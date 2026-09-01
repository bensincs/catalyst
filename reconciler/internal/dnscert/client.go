package dnscert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/inception42/cortex/shared"
)

// Options addresses the tenant's own subscription — the zone is created
// alongside the rest of the footprint, not in the platform's subscription.
type Options struct {
	SubscriptionID string
	ResourceGroup  string
	// ACMEDirectory selects the CA; blank means Let's Encrypt production.
	ACMEDirectory string
	ACMEEmail     string
}

// Client manages one tenant's DNS zone and wildcard certificate.
type Client struct {
	cred           azcore.TokenCredential
	http           *http.Client
	subscriptionID string
	resourceGroup  string
	acmeDirectory  string
	acmeEmail      string

	// accountKey is the ACME registration, kept for the process lifetime so
	// renewals reuse one account instead of registering afresh each time.
	accountKey string

	// nextIssue backs off after a failed issuance. The reconcile loop runs every
	// poll interval and Let's Encrypt rate-limits hard, so retrying at loop
	// cadence would get the tenant throttled.
	nextIssue time.Time

	// cert is the wildcard currently held. Cached in memory: the cluster Secret
	// is the durable copy, and losing this only costs one re-issue.
	certPEM  string
	keyPEM   string
	notAfter time.Time
}

// New builds the DNS/cert client. Returns nil when no subscription is known,
// which disables publishing rather than failing the reconciler.
func New(cred azcore.TokenCredential, o Options) *Client {
	if strings.TrimSpace(o.SubscriptionID) == "" || strings.TrimSpace(o.ResourceGroup) == "" {
		return nil
	}
	dir := strings.TrimSpace(o.ACMEDirectory)
	if dir == "" {
		dir = LetsEncryptProduction
	}
	return &Client{
		cred:           cred,
		http:           &http.Client{Timeout: 60 * time.Second},
		subscriptionID: strings.TrimSpace(o.SubscriptionID),
		resourceGroup:  strings.TrimSpace(o.ResourceGroup),
		acmeDirectory:  dir,
		acmeEmail:      strings.TrimSpace(o.ACMEEmail),
	}
}

// Status is what the reconciler reports back about publishing, so an operator
// can see the nameservers to delegate to and whether a certificate exists.
type Status struct {
	Nameservers []string
	State       string // pending | verified | failed
	Detail      string
	CertPEM     string
	KeyPEM      string
	NotAfter    *time.Time
}

// Ensure walks the tenant to a published state for one domain:
//
//	zone exists → nameservers reported → delegation verified → wildcard record
//	→ wildcard certificate
//
// Each step gates the next. Delegation must be real before ACME is attempted:
// Let's Encrypt resolves the challenge from the public internet, so an
// undelegated zone fails validation and burns rate limit.
//
// gatewayAddress is the cluster's ingress address; the wildcard points at it.
func (c *Client) Ensure(ctx context.Context, domain, gatewayAddress string) Status {
	zone := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	st := Status{State: "pending"}
	if zone == "" {
		return Status{}
	}

	ns, err := c.EnsureZone(ctx, zone)
	if err != nil {
		slog.Warn("dns: ensure zone failed", "zone", zone, "err", trunc(err.Error()))
		return Status{State: "failed", Detail: "Couldn't create the DNS zone: " + trunc(err.Error())}
	}
	st.Nameservers = ns
	st.Detail = "Delegate " + zone + " by setting these nameservers at your registrar."

	delegated, observed, err := VerifyDelegation(ctx, zone, ns)
	if err != nil {
		st.Detail = "Couldn't resolve the zone's nameservers yet: " + trunc(err.Error())
		return st
	}
	if !delegated {
		if len(observed) > 0 {
			st.Detail = "Delegated elsewhere: " + strings.Join(observed, ", ")
		}
		return st
	}
	st.State = "verified"
	st.Detail = "Delegation confirmed."

	// The wildcard record, once the gateway has an address to point at. One
	// record covers every app the tenant will ever publish.
	if strings.TrimSpace(gatewayAddress) != "" {
		if err := c.UpsertWildcardCNAME(ctx, zone, gatewayAddress); err != nil {
			slog.Warn("dns: wildcard record failed", "zone", zone, "err", trunc(err.Error()))
		}
	}

	// The wildcard certificate.
	if !needsRenewal(c.certPEM != "" && c.keyPEM != "", nilIfZero(c.notAfter)) {
		st.CertPEM, st.KeyPEM, st.NotAfter = c.certPEM, c.keyPEM, nilIfZero(c.notAfter)
		return st
	}
	if time.Now().Before(c.nextIssue) {
		st.Detail = "Waiting before retrying certificate issuance."
		st.CertPEM, st.KeyPEM, st.NotAfter = c.certPEM, c.keyPEM, nilIfZero(c.notAfter)
		return st
	}

	slog.Info("dns: requesting wildcard certificate", "zone", zone)
	certPEM, keyPEM, notAfter, acct, err := c.IssueWildcard(ctx, zone, c.accountKey)
	if err != nil {
		slog.Warn("dns: certificate issuance failed", "zone", zone, "err", trunc(err.Error()))
		c.nextIssue = time.Now().Add(issueBackoff)
		st.Detail = "Certificate issuance failed: " + trunc(err.Error())
		st.CertPEM, st.KeyPEM, st.NotAfter = c.certPEM, c.keyPEM, nilIfZero(c.notAfter)
		return st
	}
	c.accountKey, c.certPEM, c.keyPEM, c.notAfter = acct, certPEM, keyPEM, notAfter
	slog.Info("dns: wildcard certificate issued", "zone", zone, "notAfter", notAfter.Format(time.RFC3339))

	st.CertPEM, st.KeyPEM, st.NotAfter = certPEM, keyPEM, &notAfter
	return st
}

// AdoptCertificate seeds the in-memory certificate from one already present in
// the cluster, so a reconciler restart doesn't re-issue a perfectly good cert
// (and walk into an ACME rate limit doing it).
func (c *Client) AdoptCertificate(certPEM, keyPEM string, notAfter time.Time) {
	if c == nil || certPEM == "" || keyPEM == "" {
		return
	}
	if c.certPEM == "" || notAfter.After(c.notAfter) {
		c.certPEM, c.keyPEM, c.notAfter = certPEM, keyPEM, notAfter
	}
}

// ToClusterStatus folds publishing state into the heartbeat the control plane
// records, so the console can show nameservers and certificate expiry without
// the control plane doing any DNS work itself.
func (s Status) ToClusterStatus(cs *shared.ClusterStatus) {
	cs.DNSState = s.State
	cs.DNSDetail = s.Detail
	cs.DNSNameservers = s.Nameservers
	if s.NotAfter != nil {
		v := s.NotAfter.UTC().Format(time.RFC3339)
		cs.TLSExpiresAt = v
	}
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ensureResourceGroupExists is intentionally absent: the zone is created in the
// footprint's own resource group, which the footprint already made.

func trunc(s string) string {
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

// unusedJSON keeps encoding/json referenced for the ARM bodies in dns.go.
var _ = json.Marshal
var _ = fmt.Sprintf
