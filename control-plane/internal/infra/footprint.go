package infra

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/inception42/cortex/control-plane/internal/store"
)

// footprintTemplate is the per-tenant footprint (reconciler + Foundry + AKS),
// compiled from onboarding/footprint.bicep. The control plane deploys it into
// each enabled, delegated tenant's subscription — the customer never runs it.
//
//go:embed footprint.json
var footprintTemplate []byte

const (
	subsAPIVersion      = "2022-12-01" // Microsoft.Resources subscriptions
	rgAPIVersion        = "2021-04-01" // Microsoft.Resources/resourceGroups
	providersAPIVersion = "2021-04-01" // Microsoft.Resources/providers
	footprintName       = "cortex-footprint"
)

// footprintProviders are the resource providers the footprint (and the AKS
// node resources it creates) need registered in a delegated subscription.
// Cross-tenant deployments fail pre-flight ("The following resource provider(s)
// … are not registered") until they are — and a freshly-delegated customer sub
// usually hasn't registered them. Contributor (granted by the Lighthouse
// delegation) can register them, so the control plane does it itself rather than
// asking the customer to. Registering an already-registered provider is a no-op.
var footprintProviders = []string{
	"Microsoft.CognitiveServices",   // Foundry account/project/deployments
	"Microsoft.ContainerService",    // AKS
	"Microsoft.App",                 // reconciler container app + environment
	"Microsoft.ManagedIdentity",     // reconciler user-assigned identity
	"Microsoft.OperationalInsights", // Log Analytics (container app env)
	"Microsoft.Compute",             // AKS node VMSS
	"Microsoft.Network",             // AKS networking
	"Microsoft.Storage",             // AKS/agent storage
}

// registerProviders registers the given resource providers in the subscription
// (idempotent, best-effort). ARM registration is asynchronous — a provider moves
// Registering → Registered within seconds — so a submit in the same sweep may
// still be rejected; the next sweep then succeeds.
func (p *Provisioner) registerProviders(ctx context.Context, sub string, namespaces []string) {
	for _, ns := range namespaces {
		url := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/%s/register?api-version=%s", sub, ns, providersAPIVersion)
		if err := p.arm(ctx, http.MethodPost, url, nil, nil); err != nil {
			slog.Warn("provision: register provider failed", "sub", sub, "provider", ns, "err", trunc(err.Error()))
		}
	}
}

// discover lists the subscriptions delegated to the Cortex platform tenant via
// Lighthouse and registers each as a (disabled) tenant, recording its
// subscription + that the delegation is reachable. A platform admin still has to
// enable a tenant before its footprint is provisioned.
func (p *Provisioner) discover(ctx context.Context) {
	subs, err := p.listManagedSubscriptions(ctx)
	if err != nil {
		slog.Warn("provision: list subscriptions failed", "err", trunc(err.Error()))
		return
	}
	for _, s := range subs {
		slug, err := p.store.RecordDelegatedTenant(ctx, s.tenantID, s.name, s.subscriptionID)
		if err != nil {
			slog.Warn("provision: record delegated tenant failed", "sub", s.subscriptionID, "err", trunc(err.Error()))
			continue
		}
		_ = p.store.SetInfraDelegation(ctx, slug, true, "Delegated — Cortex manages this subscription via Azure Lighthouse.")
	}
}

type managedSub struct {
	subscriptionID string
	tenantID       string // the customer's home tenant
	name           string
}

// listManagedSubscriptions returns the subscriptions the platform SP manages
// (i.e. that have delegated to the platform tenant).
func (p *Provisioner) listManagedSubscriptions(ctx context.Context) ([]managedSub, error) {
	var body struct {
		Value []struct {
			SubscriptionID   string `json:"subscriptionId"`
			DisplayName      string `json:"displayName"`
			TenantID         string `json:"tenantId"`
			ManagedByTenants []struct {
				TenantID string `json:"tenantId"`
			} `json:"managedByTenants"`
		} `json:"value"`
	}
	url := "https://management.azure.com/subscriptions?api-version=" + subsAPIVersion
	if err := p.arm(ctx, http.MethodGet, url, nil, &body); err != nil {
		return nil, err
	}
	var out []managedSub
	for _, s := range body.Value {
		managed := false
		for _, m := range s.ManagedByTenants {
			if strings.EqualFold(m.TenantID, p.managingTenantID) {
				managed = true
				break
			}
		}
		if !managed {
			continue
		}
		out = append(out, managedSub{subscriptionID: s.SubscriptionID, tenantID: s.TenantID, name: s.DisplayName})
	}
	return out, nil
}

// provisionFootprints deploys the footprint into every enabled tenant that has a
// delegated subscription but no ready footprint yet.
func (p *Provisioner) provisionFootprints(ctx context.Context) {
	targets, err := p.store.FootprintTargets(ctx)
	if err != nil {
		slog.Warn("provision: list footprint targets failed", "err", trunc(err.Error()))
		return
	}
	for _, t := range targets {
		p.ensureFootprint(ctx, t)
	}
}

// ensureFootprint is idempotent + non-blocking, mirroring app-infra provisioning:
// if the footprint deployment succeeded it records ready; if absent it creates the
// RG + submits it (provisioning); a failed deployment is recorded failed.
func (p *Provisioner) ensureFootprint(ctx context.Context, t store.FootprintTarget) {
	url := p.footprintDeploymentURL(t.SubscriptionID)
	if t.Reprovision {
		// Platform admin requested a re-submit over an existing footprint. Consume
		// the one-shot flag now so it fires exactly once, then fall through to a
		// fresh (idempotent, Incremental) submit instead of the short-circuit below.
		_ = p.store.ClearFootprintReprovision(ctx, t.Slug)
		slog.Info("provision: re-provisioning footprint", "tenant", t.Slug)
	} else if _, state, found := p.deploymentState(ctx, url); found {
		switch {
		case strings.EqualFold(state, "Succeeded"):
			_ = p.store.SetFootprintState(ctx, t.Slug, "ready", "Reconciler + Foundry provisioned.")
		case strings.EqualFold(state, "Failed") || strings.EqualFold(state, "Canceled"):
			_ = p.store.SetFootprintState(ctx, t.Slug, "failed", "Footprint deployment "+state+".")
		default:
			_ = p.store.SetFootprintState(ctx, t.Slug, "provisioning", "Provisioning reconciler + Foundry…")
		}
		return
	}
	// A freshly delegated subscription usually hasn't registered the resource
	// providers the footprint uses — register them (idempotent) before deploying.
	p.registerProviders(ctx, t.SubscriptionID, footprintProviders)
	if err := p.createResourceGroup(ctx, t.SubscriptionID, p.footprintRG); err != nil {
		slog.Warn("provision: create footprint RG failed", "tenant", t.Slug, "err", trunc(err.Error()))
		return
	}
	if err := p.submitFootprint(ctx, t.SubscriptionID, t.Name); err != nil {
		slog.Warn("provision: submit footprint failed", "tenant", t.Slug, "err", err.Error())
		_ = p.store.SetFootprintState(ctx, t.Slug, "failed", trunc(err.Error()))
		return
	}
	_ = p.store.SetFootprintState(ctx, t.Slug, "provisioning", "Provisioning reconciler + Foundry…")
}

func (p *Provisioner) footprintDeploymentURL(sub string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Resources/deployments/%s?api-version=%s",
		sub, p.footprintRG, footprintName, infraAPIVersion)
}

// createResourceGroup PUTs a resource group (idempotent) into the customer sub.
func (p *Provisioner) createResourceGroup(ctx context.Context, sub, rg string) error {
	url := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourcegroups/%s?api-version=%s", sub, rg, rgAPIVersion)
	body, _ := json.Marshal(map[string]any{"location": p.region})
	return p.arm(ctx, http.MethodPut, url, body, nil)
}

// submitFootprint deploys the footprint template with the platform's parameters.
func (p *Provisioner) submitFootprint(ctx context.Context, sub, tenantName string) error {
	var template map[string]any
	if err := json.Unmarshal(footprintTemplate, &template); err != nil {
		return fmt.Errorf("footprint template invalid: %w", err)
	}
	params := map[string]any{
		"tenantName":      map[string]any{"value": firstNonEmpty(tenantName, "Cortex tenant")},
		"controlPlaneUrl": map[string]any{"value": p.controlPlaneURL},
		"cortexApiScope":  map[string]any{"value": p.apiScope},
		"reconcilerImage": map[string]any{"value": p.reconcilerImage},
	}
	payload, err := json.Marshal(map[string]any{
		"properties": map[string]any{"mode": "Incremental", "template": template, "parameters": params},
	})
	if err != nil {
		return err
	}
	return p.arm(ctx, http.MethodPut, p.footprintDeploymentURL(sub), payload, nil)
}

// deploymentState reads a deployment's provisioning state (found=false when it
// doesn't exist yet).
func (p *Provisioner) deploymentState(ctx context.Context, url string) (map[string]any, string, bool) {
	var body struct {
		Properties struct {
			ProvisioningState string                         `json:"provisioningState"`
			Outputs           map[string]struct{ Value any } `json:"outputs"`
		} `json:"properties"`
	}
	if err := p.arm(ctx, http.MethodGet, url, nil, &body); err != nil {
		return nil, "", false
	}
	outs := make(map[string]any, len(body.Properties.Outputs))
	for k, v := range body.Properties.Outputs {
		outs[k] = v.Value
	}
	return outs, body.Properties.ProvisioningState, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
