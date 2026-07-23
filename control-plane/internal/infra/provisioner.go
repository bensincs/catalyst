// Package infra provisions each enabled deployment's Azure infrastructure (its
// resolved ARM template) from the control plane, cross-tenant, into the customer's
// Azure Lighthouse-delegated resource group. The platform service principal
// authenticates in the platform tenant; Lighthouse authorizes it on the customer's
// cortex-infra RG. Outputs are stored so SyncDesired can merge them into the Helm
// values before the reconciler stamps the chart.
package infra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/inception42/cortex/control-plane/internal/store"
)

const (
	armScope        = "https://management.azure.com/.default"
	infraAPIVersion = "2021-04-01" // Microsoft.Resources/deployments

	stateProvisioning = "provisioning"
	stateReady        = "ready"
	stateFailed       = "failed"
)

// Provisioner drives all cross-tenant Azure work from the control plane, via
// Azure Lighthouse: it discovers newly-delegated subscriptions, provisions the
// per-tenant footprint (reconciler + Foundry + AKS) into enabled tenants, and
// provisions each deployment's application infra.
type Provisioner struct {
	cred  azcore.TokenCredential
	http  *http.Client
	store *store.Store

	managingTenantID string // the Cortex platform tenant (filters delegated subs)
	infraRG          string // RG for per-deployment application infra
	footprintRG      string // RG for the per-tenant footprint
	region           string // region for created resource groups
	controlPlaneURL  string // reconciler → control plane
	apiScope         string // Entra scope for the control-plane API
	reconcilerImage  string // reconciler container image

	mu          sync.Mutex        // guards apiVersions
	apiVersions map[string]string // provider/type → api-version, resolved on demand for teardown
}

// Config enables cross-tenant provisioning + supplies the footprint parameters.
// The control plane authenticates with its own managed identity (or AZURE_* env /
// az login locally) — no secret is held here.
type Config struct {
	Enabled            bool
	ManagingTenantID   string // the Cortex platform tenant (filters delegated subs)
	InfraResourceGroup string
	FootprintRG        string
	Region             string
	ControlPlaneURL    string
	APIScope           string
	ReconcilerImage    string
}

// New builds a Provisioner, or (nil, nil) when cross-tenant provisioning is off.
// The credential is DefaultAzureCredential — the control plane's managed identity
// when it runs in Azure, falling back to AZURE_* env or az login for local runs.
func New(st *store.Store, cfg Config) (*Provisioner, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	return &Provisioner{
		cred:             cred,
		http:             &http.Client{Timeout: 90 * time.Second},
		store:            st,
		managingTenantID: cfg.ManagingTenantID,
		infraRG:          cfg.InfraResourceGroup,
		footprintRG:      cfg.FootprintRG,
		region:           cfg.Region,
		controlPlaneURL:  cfg.ControlPlaneURL,
		apiScope:         cfg.APIScope,
		reconcilerImage:  cfg.ReconcilerImage,
		apiVersions:      map[string]string{},
	}, nil
}

// Run sweeps every interval until ctx is cancelled.
func (p *Provisioner) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	p.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.reconcile(ctx)
		}
	}
}

func (p *Provisioner) reconcile(ctx context.Context) {
	// 1. Discover delegated subscriptions → register (disabled) tenants.
	p.discover(ctx)
	// 2. Provision the footprint into ENABLED tenants that don't have it yet.
	p.provisionFootprints(ctx)
	// 3. Provision each enabled deployment's application infra.
	targets, err := p.store.InfraTargets(ctx)
	if err != nil {
		slog.Warn("infra: list targets failed", "err", err)
		return
	}
	for _, tgt := range targets {
		p.ensure(ctx, tgt)
	}
	// 4. Tear down the Azure resources of infra that was disabled/deleted.
	p.teardown(ctx)
}

// ensure is idempotent + non-blocking: if the deployment already succeeded it
// records outputs (ready); if it's absent it submits it (provisioning); a failed
// deployment is recorded failed. A submit error is left to retry next sweep.
func (p *Provisioner) ensure(ctx context.Context, tgt store.InfraTarget) {
	name := deploymentName(tgt.InfraID)
	if outs, pstate, found := p.deploymentState(ctx, p.deploymentURL(tgt.SubscriptionID, name)); found {
		switch {
		case strings.EqualFold(pstate, "Succeeded"):
			_ = p.store.SetInfraState(ctx, tgt.TenantSlug, tgt.InfraID, stateReady, outs)
		case strings.EqualFold(pstate, "Failed") || strings.EqualFold(pstate, "Canceled"):
			_ = p.store.SetInfraState(ctx, tgt.TenantSlug, tgt.InfraID, stateFailed, nil)
		default:
			_ = p.store.SetInfraState(ctx, tgt.TenantSlug, tgt.InfraID, stateProvisioning, nil)
		}
		return
	}
	// Substitute per-tenant tokens (e.g. {{tenantHash}} for a globally-unique Key
	// Vault name) into the template before deploying, so a single platform-authored
	// infra yields tenant-unique resource names instead of colliding across tenants.
	armStr := substituteTokens(tgt.ArmTemplate, tgt.TenantSlug, p.region)
	var template map[string]any
	if err := json.Unmarshal([]byte(armStr), &template); err != nil {
		slog.Warn("infra: template is not valid ARM JSON; skipping", "infra", tgt.InfraID)
		_ = p.store.SetInfraState(ctx, tgt.TenantSlug, tgt.InfraID, stateFailed, nil)
		return
	}
	// The customer sub only has the footprint RG from onboarding, and a fresh sub
	// won't have the app-infra's resource providers registered either. Create the
	// app-infra RG and register the template's providers (both idempotent) before
	// deploying into them — otherwise the deployment 404s (ResourceGroupNotFound)
	// or is rejected for an unregistered provider.
	p.registerProviders(ctx, tgt.SubscriptionID, templateProviders(template))
	if err := p.createResourceGroup(ctx, tgt.SubscriptionID, p.infraRG); err != nil {
		slog.Warn("infra: create resource group failed", "tenant", tgt.TenantSlug, "err", trunc(err.Error()))
		return
	}
	if err := p.submit(ctx, tgt.SubscriptionID, name, template); err != nil {
		slog.Warn("infra: submit deployment failed", "infra", tgt.InfraID, "tenant", tgt.TenantSlug, "err", trunc(err.Error()))
		return
	}
	_ = p.store.SetInfraState(ctx, tgt.TenantSlug, tgt.InfraID, stateProvisioning, nil)
}

// templateProviders returns the distinct resource-provider namespaces an ARM
// template uses (e.g. "Microsoft.DBforPostgreSQL"), recursing into nested
// Microsoft.Resources/deployments (AVM modules compile to a nested deployment, so
// the real resource types live one level down). Used to register a fresh
// subscription's providers before deploying.
func templateProviders(template map[string]any) []string {
	seen := map[string]bool{}
	collectProviders(template, seen)
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	return out
}

func collectProviders(template map[string]any, seen map[string]bool) {
	res, _ := template["resources"].([]any)
	for _, r := range res {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "" {
			if i := strings.IndexByte(t, '/'); i > 0 {
				seen[t[:i]] = true
			}
		}
		// Recurse into a nested deployment's template (AVM modules).
		if props, ok := m["properties"].(map[string]any); ok {
			if nested, ok := props["template"].(map[string]any); ok {
				collectProviders(nested, seen)
			}
		}
	}
}

func (p *Provisioner) deploymentURL(sub, name string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Resources/deployments/%s?api-version=%s",
		sub, p.infraRG, name, infraAPIVersion)
}

func (p *Provisioner) submit(ctx context.Context, sub, name string, template map[string]any) error {
	payload, err := json.Marshal(map[string]any{
		"properties": map[string]any{"mode": "Incremental", "template": template},
	})
	if err != nil {
		return err
	}
	return p.arm(ctx, http.MethodPut, p.deploymentURL(sub, name), payload, nil)
}

// arm makes a Bearer-authenticated ARM call, decoding JSON into out when non-nil.
func (p *Provisioner) arm(ctx context.Context, method, url string, body []byte, out any) error {
	tok, err := p.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return fmt.Errorf("acquire ARM token: %w", err)
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("arm %s %d: %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// deploymentName maps an app id to a valid ARM deployment name.
func deploymentName(appID string) string {
	return "cortex-app-" + strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(appID)
}

func trunc(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// ── teardown ──────────────────────────────────────────────────────────────

// teardown processes the queue of infrastructure that was disabled or deleted,
// removing the Azure resources their ARM deployments created. Idempotent + safe:
// it only touches resources a Cortex deployment (cortex-app-<id>) created, and a
// transient failure leaves the queue row for the next sweep.
func (p *Provisioner) teardown(ctx context.Context) {
	tds, err := p.store.InfraTeardownTargets(ctx)
	if err != nil {
		slog.Warn("infra: list teardowns failed", "err", trunc(err.Error()))
		return
	}
	for _, td := range tds {
		p.deprovision(ctx, td)
	}
}

func (p *Provisioner) deprovision(ctx context.Context, td store.InfraTeardown) {
	if td.SubscriptionID == "" {
		_ = p.store.FinalizeInfraTeardown(ctx, td.TenantSlug, td.InfraID)
		return
	}
	name := deploymentName(td.InfraID)
	resources, found, err := p.deploymentResources(ctx, td.SubscriptionID, name)
	if err != nil {
		slog.Warn("infra: read deployment for teardown failed", "infra", td.InfraID, "err", trunc(err.Error()))
		return // transient — retry next sweep, never clear on uncertainty
	}
	if !found {
		// Never provisioned, or already gone — nothing to delete.
		_ = p.store.FinalizeInfraTeardown(ctx, td.TenantSlug, td.InfraID)
		return
	}
	allGone := true
	for _, id := range resources {
		if err := p.deleteResource(ctx, id); err != nil {
			// A child resource (e.g. an AVM advancedThreatProtectionSettings) is
			// removed when its parent is deleted, and some child types aren't even
			// listed in the provider metadata we resolve the api-version from — so
			// never let one wedge the teardown. The parent (a top-level resource
			// that IS resolvable) gates completion; the child goes with it.
			if isNestedResource(id) {
				slog.Warn("infra: skipping nested child resource (removed with its parent)", "infra", td.InfraID, "resource", id, "err", trunc(err.Error()))
				continue
			}
			allGone = false
			slog.Warn("infra: delete resource failed", "infra", td.InfraID, "resource", id, "err", trunc(err.Error()))
		}
	}
	if !allGone {
		return // retry the stragglers next sweep
	}
	// Resources deleted → drop the deployment records (metadata) + clear the queue.
	_ = p.armDelete(ctx, p.deploymentURL(td.SubscriptionID, name))
	_ = p.armDelete(ctx, p.deploymentURL(td.SubscriptionID, name+"-m"))
	_ = p.store.FinalizeInfraTeardown(ctx, td.TenantSlug, td.InfraID)
	slog.Info("infra: deprovisioned", "infra", td.InfraID, "tenant", td.TenantSlug, "resources", len(resources))
}

// deploymentResources returns the resource ids a deployment created. found is
// false only on a real 404 (so a transient error is never mistaken for "gone").
func (p *Provisioner) deploymentResources(ctx context.Context, sub, name string) ([]string, bool, error) {
	tok, err := p.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return nil, false, fmt.Errorf("acquire ARM token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.deploymentURL(sub, name), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("get deployment %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var body struct {
		Properties struct {
			OutputResources []struct {
				ID string `json:"id"`
			} `json:"outputResources"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, err
	}
	ids := make([]string, 0, len(body.Properties.OutputResources))
	for _, r := range body.Properties.OutputResources {
		if strings.TrimSpace(r.ID) != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids, true, nil
}

// deleteResource deletes an Azure resource by id, resolving the provider's
// api-version. A 404 (already gone) counts as success.
func (p *Provisioner) deleteResource(ctx context.Context, resourceID string) error {
	ver, err := p.resourceAPIVersion(ctx, resourceID)
	if err != nil {
		return err
	}
	return p.armDelete(ctx, "https://management.azure.com"+resourceID+"?api-version="+ver)
}

// armDelete issues a DELETE, tolerating 404 (already gone) and 202 (async).
func (p *Provisioner) armDelete(ctx context.Context, url string) error {
	tok, err := p.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("delete %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// resourceAPIVersion resolves (and caches) a valid api-version for a resource id
// from its provider's metadata.
func (p *Provisioner) resourceAPIVersion(ctx context.Context, resourceID string) (string, error) {
	sub, ns, rtype, ok := parseResourceID(resourceID)
	if !ok {
		return "", fmt.Errorf("unparseable resource id: %s", resourceID)
	}
	key := strings.ToLower(ns + "/" + rtype)
	p.mu.Lock()
	if v, hit := p.apiVersions[key]; hit {
		p.mu.Unlock()
		return v, nil
	}
	p.mu.Unlock()

	var body struct {
		ResourceTypes []struct {
			ResourceType      string   `json:"resourceType"`
			APIVersions       []string `json:"apiVersions"`
			DefaultAPIVersion string   `json:"defaultApiVersion"`
		} `json:"resourceTypes"`
	}
	url := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/%s?api-version=2021-04-01", sub, ns)
	if err := p.arm(ctx, http.MethodGet, url, nil, &body); err != nil {
		return "", err
	}
	for _, rt := range body.ResourceTypes {
		if !strings.EqualFold(rt.ResourceType, rtype) {
			continue
		}
		ver := rt.DefaultAPIVersion
		if ver == "" && len(rt.APIVersions) > 0 {
			ver = rt.APIVersions[0] // provider lists newest first
		}
		if ver == "" {
			return "", fmt.Errorf("no api-version for %s/%s", ns, rtype)
		}
		p.mu.Lock()
		p.apiVersions[key] = ver
		p.mu.Unlock()
		return ver, nil
	}
	return "", fmt.Errorf("resource type %s/%s not found in provider metadata", ns, rtype)
}

// isNestedResource reports whether an ARM id addresses a child resource — its
// type has more than one segment (e.g. flexibleServers/advancedThreatProtectionSettings).
// Such children are deleted along with their top-level parent, so the teardown
// must not stall on one whose child type the provider doesn't enumerate.
func isNestedResource(id string) bool {
	_, _, rtype, ok := parseResourceID(id)
	return ok && strings.Contains(rtype, "/")
}

// parseResourceID pulls the subscription, provider namespace, and (possibly
// nested) resource type out of an ARM resource id.
func parseResourceID(id string) (sub, ns, rtype string, ok bool) {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	provIdx := -1
	for i, seg := range parts {
		if strings.EqualFold(seg, "subscriptions") && i+1 < len(parts) {
			sub = parts[i+1]
		}
		if strings.EqualFold(seg, "providers") {
			provIdx = i
			break
		}
	}
	if provIdx < 0 || provIdx+2 >= len(parts) {
		return "", "", "", false
	}
	ns = parts[provIdx+1]
	// After the namespace: type, name, [subtype, subname, …] — the types are the
	// even-indexed segments.
	rest := parts[provIdx+2:]
	var types []string
	for i := 0; i < len(rest); i += 2 {
		types = append(types, rest[i])
	}
	rtype = strings.Join(types, "/")
	if sub == "" || ns == "" || rtype == "" {
		return "", "", "", false
	}
	return sub, ns, rtype, true
}

// substituteTokens replaces per-tenant template tokens in a resolved ARM template
// before it's deployed into a tenant's subscription. Tokens survive Bicep
// compilation as literal text (they use `{{…}}`, not Bicep's `${…}`), so an author
// sets e.g. `name: 'cortexkv{{tenantHash}}'` once at the platform level and each
// tenant gets a unique name:
//
//	{{tenant}}     — the tenant slug (e.g. t-cff8707ddd78)
//	{{tenantHash}} — a short, stable hash of the slug (safe for length/charset-limited names)
//	{{region}}     — the deployment region
func substituteTokens(arm, slug, region string) string {
	return strings.NewReplacer(
		"{{tenant}}", slug,
		"{{tenantHash}}", tenantHash(slug),
		"{{region}}", region,
	).Replace(arm)
}

// tenantHash is a short, stable, lowercase-alphanumeric hash of a tenant slug —
// deterministic so re-provisioning targets the same resource, and safe for names
// with tight length/character limits (Key Vault, storage accounts, …).
func tenantHash(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	return hex.EncodeToString(sum[:])[:10]
}
