package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/inception42/cortex/control-plane/internal/store"
)

// Config configures the ingress worker.
type Config struct {
	// Enabled mirrors cross-tenant provisioning: without an ARM credential there
	// is nothing this worker can do.
	Enabled bool
	// SubscriptionID / ResourceGroup are where tenant DNS zones live — the
	// PLATFORM subscription, so no cross-directory grant is ever needed.
	SubscriptionID string
	ResourceGroup  string
	// ACMEDirectory selects the CA. Staging while proving things out; production
	// otherwise.
	ACMEDirectory string
	ACMEEmail     string
}

// Manager keeps each tenant's DNS zone and wildcard certificate correct.
type Manager struct {
	cred           azcore.TokenCredential
	http           *http.Client
	store          *store.Store
	subscriptionID string
	resourceGroup  string
	acmeDirectory  string
	acmeEmail      string

	// nextIssue throttles certificate attempts per tenant. The sweep runs every
	// 30s, but Let's Encrypt rate-limits hard (and bans on sustained abuse), so a
	// failing tenant must back off rather than retry on every pass.
	mu        sync.Mutex
	nextIssue map[string]time.Time
}

// issueBackoff is how long to wait after a failed issuance before trying again.
const issueBackoff = 15 * time.Minute

// New builds the ingress manager. Returns (nil, nil) when disabled or
// unconfigured, matching the infra provisioner's contract.
func New(st *store.Store, cfg Config) (*Manager, error) {
	if !cfg.Enabled || strings.TrimSpace(cfg.SubscriptionID) == "" {
		return nil, nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	dir := strings.TrimSpace(cfg.ACMEDirectory)
	if dir == "" {
		dir = LetsEncryptProduction
	}
	rg := strings.TrimSpace(cfg.ResourceGroup)
	if rg == "" {
		rg = "cortex-dns"
	}
	return &Manager{
		nextIssue:      map[string]time.Time{},
		cred:           cred,
		http:           &http.Client{Timeout: 60 * time.Second},
		store:          st,
		subscriptionID: strings.TrimSpace(cfg.SubscriptionID),
		resourceGroup:  rg,
		acmeDirectory:  dir,
		acmeEmail:      strings.TrimSpace(cfg.ACMEEmail),
	}, nil
}

// Run sweeps every tenant with a configured domain until ctx is cancelled.
func (m *Manager) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.reconcile(ctx)
		}
	}
}

// canIssue reports whether a certificate attempt is due for this tenant.
func (m *Manager) canIssue(slug string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Now().After(m.nextIssue[slug])
}

func (m *Manager) backOff(slug string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextIssue[slug] = time.Now().Add(issueBackoff)
}

// ensureResourceGroup creates the resource group the tenant zones live in.
// Idempotent; ARM refuses to create a zone in a group that doesn't exist.
func (m *Manager) ensureResourceGroup(ctx context.Context) error {
	url := fmt.Sprintf("%s/subscriptions/%s/resourcegroups/%s?api-version=2021-04-01",
		armBase, m.subscriptionID, m.resourceGroup)
	body, _ := json.Marshal(map[string]any{"location": "global"})
	return m.armDo(ctx, http.MethodPut, url, body, nil)
}

func (m *Manager) reconcile(ctx context.Context) {
	targets, err := m.store.IngressTargets(ctx)
	if err != nil {
		slog.Warn("ingress: list targets failed", "err", trunc(err.Error()))
		return
	}
	if len(targets) == 0 {
		return
	}
	if err := m.ensureResourceGroup(ctx); err != nil {
		slog.Warn("ingress: ensure DNS resource group failed", "rg", m.resourceGroup, "err", trunc(err.Error()))
		return
	}
	for _, t := range targets {
		m.ensure(ctx, t)
	}
}

// ensure walks one tenant to a published state:
//
//	zone exists → delegation verified → wildcard record → wildcard certificate
//
// Each step gates the next. Delegation in particular must be real before ACME is
// attempted: Let's Encrypt resolves the challenge from the public internet, so
// an undelegated zone fails validation and burns rate limit.
func (m *Manager) ensure(ctx context.Context, t store.IngressTarget) {
	zone := t.AppsDomain

	// 1. The zone, and the nameservers the customer needs.
	ns := t.Nameservers
	if len(ns) == 0 || t.DNSState == "" {
		got, err := m.EnsureZone(ctx, zone)
		if err != nil {
			slog.Warn("ingress: ensure zone failed", "tenant", t.Slug, "zone", zone, "err", trunc(err.Error()))
			_ = m.store.SetDNSState(ctx, t.Slug, "failed", "Couldn't create the DNS zone: "+trunc(err.Error()), nil)
			return
		}
		ns = got
		_ = m.store.SetDNSState(ctx, t.Slug, "pending",
			"Zone created. Delegate "+zone+" by setting these nameservers at your registrar.", ns)
		slog.Info("ingress: zone ready", "tenant", t.Slug, "zone", zone, "nameservers", len(ns))
	}

	// 2. Delegation — observed, not asserted.
	delegated, observed, err := VerifyDelegation(ctx, zone, ns)
	if err != nil {
		_ = m.store.SetDNSState(ctx, t.Slug, "pending", "Couldn't resolve the zone's nameservers yet: "+trunc(err.Error()), nil)
		return
	}
	if !delegated {
		detail := "Waiting for delegation — the zone's parent doesn't point at our nameservers yet."
		if len(observed) > 0 {
			detail = "Delegated elsewhere: " + strings.Join(observed, ", ")
		}
		_ = m.store.SetDNSState(ctx, t.Slug, "pending", detail, nil)
		return
	}
	if t.DNSState != "verified" {
		_ = m.store.SetDNSState(ctx, t.Slug, "verified", "Delegation confirmed.", nil)
		slog.Info("ingress: delegation verified", "tenant", t.Slug, "zone", zone)
	}

	// 3. The wildcard record, once the cluster has an address to point at.
	if t.GatewayIP != "" {
		if err := m.UpsertWildcardCNAME(ctx, zone, t.GatewayIP); err != nil {
			slog.Warn("ingress: wildcard record failed", "tenant", t.Slug, "err", trunc(err.Error()))
		}
	}

	// 4. The wildcard certificate.
	if !needsRenewal(t.HasCert, t.CertExpires) {
		return
	}
	if !m.canIssue(t.Slug) {
		return // backing off after a failure; ACME rate limits are unforgiving
	}
	slog.Info("ingress: requesting wildcard certificate", "tenant", t.Slug, "zone", zone)
	certPEM, keyPEM, notAfter, acctKey, err := m.IssueWildcard(ctx, zone, t.ACMEKey)
	if err != nil {
		slog.Warn("ingress: certificate issuance failed", "tenant", t.Slug, "zone", zone, "err", trunc(err.Error()))
		_ = m.store.SetTLSDetail(ctx, t.Slug, "Certificate issuance failed: "+trunc(err.Error()))
		m.backOff(t.Slug)
		return
	}
	if acctKey != t.ACMEKey {
		_ = m.store.SetACMEAccountKey(ctx, t.Slug, acctKey)
	}
	if err := m.store.SetTLSCertificate(ctx, t.Slug, certPEM, keyPEM, notAfter); err != nil {
		slog.Warn("ingress: storing certificate failed", "tenant", t.Slug, "err", trunc(err.Error()))
		return
	}
	slog.Info("ingress: wildcard certificate issued", "tenant", t.Slug, "zone", zone, "notAfter", notAfter.Format(time.RFC3339))
}

func trunc(s string) string {
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
