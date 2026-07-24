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
	subsAPIVersion            = "2022-12-01" // Microsoft.Resources subscriptions
	rgAPIVersion              = "2021-04-01" // Microsoft.Resources/resourceGroups
	providersAPIVersion       = "2021-04-01" // Microsoft.Resources/providers
	featuresAPIVersion        = "2021-07-01" // Microsoft.Features
	managedIdentityAPIVersion = "2023-01-31" // Microsoft.ManagedIdentity/userAssignedIdentities
	footprintName             = "cortex-footprint"

	// hostingPlatform matches model.HostingPlatform — a tenant hosted in the
	// platform's own subscription (a dedicated RG per tenant).
	hostingPlatform = "platform"
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
	"Microsoft.ServiceNetworking",   // Application Gateway for Containers (trafficControllers)
	"Microsoft.NetworkFunction",     // AGC dependency
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

// footprintFeatures are the preview feature flags the footprint's AKS add-ons
// need (Application Gateway for Containers + its managed Gateway API). They must
// be registered before Microsoft.ContainerService is (re)registered to take
// effect, so registerFeatures runs first in the footprint sweep.
var footprintFeatures = []struct{ ns, name string }{
	{"Microsoft.ContainerService", "ManagedGatewayAPIPreview"},
	{"Microsoft.ContainerService", "ApplicationLoadBalancerPreview"},
}

// registerFeatures registers the preview features (idempotent, best-effort).
// Registration is asynchronous and only takes effect after the owning provider is
// re-registered — both of which the footprint sweep retries until the deploy
// succeeds.
func (p *Provisioner) registerFeatures(ctx context.Context, sub string) {
	for _, f := range footprintFeatures {
		url := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.Features/providers/%s/features/%s/register?api-version=%s",
			sub, f.ns, f.name, featuresAPIVersion)
		if err := p.arm(ctx, http.MethodPost, url, nil, nil); err != nil {
			slog.Warn("provision: register feature failed", "sub", sub, "feature", f.name, "err", trunc(err.Error()))
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
// RG + submits it (provisioning); a failed deployment is recorded failed. Works
// for both delegated tenants (customer subscription via Lighthouse) and
// platform-hosted ones (the platform's own subscription, a dedicated RG).
func (p *Provisioner) ensureFootprint(ctx context.Context, t store.FootprintTarget) {
	rg := firstNonEmpty(t.ResourceGroup, p.footprintRG)
	region := firstNonEmpty(t.Region, p.region)
	url := p.footprintDeploymentURL(t.SubscriptionID, rg)
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
	p.registerFeatures(ctx, t.SubscriptionID)
	p.registerProviders(ctx, t.SubscriptionID, footprintProviders)
	if err := p.createResourceGroup(ctx, t.SubscriptionID, rg, region); err != nil {
		slog.Warn("provision: create footprint RG failed", "tenant", t.Slug, "err", trunc(err.Error()))
		return
	}
	// Platform-hosted: pre-create the reconciler managed identity so the control
	// plane knows its principal up front (the oid the reconciler is authorized by,
	// since its token tid is the shared platform directory) and can pass it into
	// the footprint instead of the template minting one.
	reconIdentityResourceID := ""
	isDelegated := t.HostingMode != hostingPlatform
	if !isDelegated {
		id, err := p.ensureReconcilerIdentity(ctx, t.SubscriptionID, rg, region)
		if err != nil {
			slog.Warn("provision: pre-create reconciler identity failed", "tenant", t.Slug, "err", trunc(err.Error()))
			_ = p.store.SetFootprintState(ctx, t.Slug, "provisioning", "Preparing reconciler identity…")
			return
		}
		reconIdentityResourceID = id.resourceID
		if id.principalID != "" && id.principalID != t.ReconcilerPrincipalID {
			_ = p.store.SetReconcilerPrincipal(ctx, t.Slug, id.principalID)
		}
	}
	if err := p.submitFootprint(ctx, t.SubscriptionID, rg, t, reconIdentityResourceID, isDelegated); err != nil {
		slog.Warn("provision: submit footprint failed", "tenant", t.Slug, "err", err.Error())
		_ = p.store.SetFootprintState(ctx, t.Slug, "failed", trunc(err.Error()))
		return
	}
	_ = p.store.SetFootprintState(ctx, t.Slug, "provisioning", "Provisioning reconciler + Foundry…")
}

// reconcilerIdentity is a pre-created user-assigned managed identity's key ids.
type reconcilerIdentity struct {
	resourceID  string
	principalID string
	clientID    string
}

const reconcilerIdentityName = "cortex-recon"

// ensureReconcilerIdentity creates (idempotent) the reconciler's user-assigned
// managed identity in the tenant's resource group and returns its ids. Used for
// platform-hosted tenants so the control plane records the identity's principal
// (the oid its reconciler is authorized by) before deploying the footprint.
func (p *Provisioner) ensureReconcilerIdentity(ctx context.Context, sub, rg, region string) (reconcilerIdentity, error) {
	if region == "" {
		region = p.region
	}
	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s?api-version=%s",
		sub, rg, reconcilerIdentityName, managedIdentityAPIVersion)
	body, _ := json.Marshal(map[string]any{"location": region})
	var resp struct {
		ID         string `json:"id"`
		Properties struct {
			PrincipalID string `json:"principalId"`
			ClientID    string `json:"clientId"`
		} `json:"properties"`
	}
	if err := p.arm(ctx, http.MethodPut, url, body, &resp); err != nil {
		return reconcilerIdentity{}, err
	}
	return reconcilerIdentity{
		resourceID:  resp.ID,
		principalID: resp.Properties.PrincipalID,
		clientID:    resp.Properties.ClientID,
	}, nil
}

func (p *Provisioner) footprintDeploymentURL(sub, rg string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Resources/deployments/%s?api-version=%s",
		sub, rg, footprintName, infraAPIVersion)
}

// createResourceGroup PUTs a resource group (idempotent) into a subscription.
func (p *Provisioner) createResourceGroup(ctx context.Context, sub, rg, region string) error {
	if region == "" {
		region = p.region
	}
	url := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourcegroups/%s?api-version=%s", sub, rg, rgAPIVersion)
	body, _ := json.Marshal(map[string]any{"location": region})
	return p.arm(ctx, http.MethodPut, url, body, nil)
}

// submitFootprint deploys the footprint template with the platform's parameters.
// For platform-hosted tenants it passes the pre-created reconciler identity and
// flags the deployment same-tenant (isDelegated=false). cluster_mode drives
// whether an AKS cluster is provisioned ('aks') or skipped ('byo' — bring your
// own; the footprint deploys only the reconciler + Foundry). footprint_config
// supplies optional AKS sizing (region, nodeCount, nodeVmSize).
func (p *Provisioner) submitFootprint(ctx context.Context, sub, rg string, t store.FootprintTarget, reconcilerIdentityResourceID string, isDelegated bool) error {
	var template map[string]any
	if err := json.Unmarshal(footprintTemplate, &template); err != nil {
		return fmt.Errorf("footprint template invalid: %w", err)
	}
	deployCluster := !strings.EqualFold(t.ClusterMode, "byo")
	params := map[string]any{
		"tenantName":      map[string]any{"value": firstNonEmpty(t.Name, "Cortex tenant")},
		"controlPlaneUrl": map[string]any{"value": p.controlPlaneURL},
		"cortexApiScope":  map[string]any{"value": p.apiScope},
		"reconcilerImage": map[string]any{"value": p.reconcilerImage},
		"isDelegated":     map[string]any{"value": isDelegated},
		"tenantSlug":      map[string]any{"value": t.Slug},
		"deployCluster":   map[string]any{"value": deployCluster},
	}
	if reconcilerIdentityResourceID != "" {
		params["reconcilerIdentityResourceId"] = map[string]any{"value": reconcilerIdentityResourceID}
	}
	// Optional AKS sizing from the admin-set footprint config.
	if deployCluster {
		if v := configString(t.Config, "region"); v != "" {
			params["location"] = map[string]any{"value": v}
		}
		if v := configString(t.Config, "nodeVmSize"); v != "" {
			params["nodeVmSize"] = map[string]any{"value": v}
		}
		if n := configInt(t.Config, "nodeCount"); n > 0 {
			params["nodeCount"] = map[string]any{"value": n}
		}
	}
	payload, err := json.Marshal(map[string]any{
		"properties": map[string]any{"mode": "Incremental", "template": template, "parameters": params},
	})
	if err != nil {
		return err
	}
	return p.arm(ctx, http.MethodPut, p.footprintDeploymentURL(sub, rg), payload, nil)
}

// configString reads a string field from a footprint config map (tolerant of
// missing/typed values).
func configString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// configInt reads an int field from a footprint config map (JSON numbers decode
// as float64).
func configInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
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
