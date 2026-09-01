// Package dnscert owns the tenant's public DNS zone and the wildcard certificate
// for it, from inside the tenant.
//
// Both live in the TENANT's own subscription, which is what makes this work at
// all: the reconciler's managed identity and the zone are then in the same Entra
// directory, so the footprint can grant DNS Zone Contributor on it. That holds
// for a Lighthouse-delegated tenant exactly as for a platform-hosted one. A zone
// held in the platform's subscription could not be granted to a
// customer-directory identity — the same cross-directory wall that rules out
// sharing clusters between tenants.
//
// Everything here therefore runs in-tenant: no certificate or DNS credential
// ever leaves the customer's subscription, and the control plane holds neither.
package dnscert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	dnsAPIVersion = "2018-05-01" // Microsoft.Network/dnsZones
	armScope      = "https://management.azure.com/.default"
	armBase       = "https://management.azure.com"
)

// armDo performs an authenticated ARM request. out may be nil.
func (c *Client) armDo(ctx context.Context, method, url string, body []byte, out any) error {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{armScope}})
	if err != nil {
		return err
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
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil // already gone
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("arm %s %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) zoneURL(zone string) string {
	return fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s?api-version=%s",
		armBase, c.subscriptionID, c.resourceGroup, zone, dnsAPIVersion)
}

func (c *Client) recordURL(zone, recordType, name string) string {
	return fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s/%s?api-version=%s",
		armBase, c.subscriptionID, c.resourceGroup, zone, recordType, name, dnsAPIVersion)
}

// EnsureZone creates the public DNS zone (idempotent) and returns the
// nameservers Azure assigned it — the values the customer must set at their
// registrar to complete delegation.
//
// Public, not private: a private zone has no nameservers and cannot be
// delegated, so it can't serve this purpose at all.
func (c *Client) EnsureZone(ctx context.Context, zone string) ([]string, error) {
	body, _ := json.Marshal(map[string]any{
		"location": "global",
		"properties": map[string]any{
			"zoneType": "Public",
		},
	})
	if err := c.armDo(ctx, http.MethodPut, c.zoneURL(zone), body, nil); err != nil {
		return nil, err
	}
	// The nameservers are assigned asynchronously; the PUT response often omits
	// them, so read the zone back.
	var got struct {
		Properties struct {
			NameServers []string `json:"nameServers"`
		} `json:"properties"`
	}
	if err := c.armDo(ctx, http.MethodGet, c.zoneURL(zone), nil, &got); err != nil {
		return nil, err
	}
	ns := make([]string, 0, len(got.Properties.NameServers))
	for _, n := range got.Properties.NameServers {
		ns = append(ns, normalizeHost(n))
	}
	sort.Strings(ns)
	return ns, nil
}

// DeleteZone removes the zone entirely. Used when a tenant clears its domain or
// is deleted.
func (c *Client) DeleteZone(ctx context.Context, zone string) error {
	return c.armDo(ctx, http.MethodDelete, c.zoneURL(zone), nil, nil)
}

// UpsertWildcardCNAME points every host under the zone at the tenant cluster's
// ingress address. One record covers every app the tenant will ever publish,
// which is why adding an app needs no DNS work.
//
// A CNAME (not A) because Application Gateway for Containers gives an FQDN
// rather than a stable address. The zone apex is deliberately left alone — a
// CNAME is illegal there, and apps always live on a subdomain.
func (c *Client) UpsertWildcardCNAME(ctx context.Context, zone, target string) error {
	body, _ := json.Marshal(map[string]any{
		"properties": map[string]any{
			"TTL":         300,
			"CNAMERecord": map[string]any{"cname": normalizeHost(target)},
		},
	})
	return c.armDo(ctx, http.MethodPut, c.recordURL(zone, "CNAME", "*"), body, nil)
}

// upsertTXT writes an ACME challenge record. Short TTL so a failed attempt
// doesn't poison the next one.
func (c *Client) upsertTXT(ctx context.Context, zone, name string, values []string) error {
	records := make([]any, 0, len(values))
	for _, v := range values {
		records = append(records, map[string]any{"value": []string{v}})
	}
	body, _ := json.Marshal(map[string]any{
		"properties": map[string]any{"TTL": 30, "TXTRecords": records},
	})
	return c.armDo(ctx, http.MethodPut, c.recordURL(zone, "TXT", name), body, nil)
}

func (c *Client) deleteTXT(ctx context.Context, zone, name string) error {
	return c.armDo(ctx, http.MethodDelete, c.recordURL(zone, "TXT", name), nil, nil)
}

// VerifyDelegation reports whether the zone's parent actually points at our
// nameservers. It resolves NS from public resolvers rather than trusting the
// customer's word, so the console can show whether delegation is genuinely live.
//
// Returns (delegated, observed nameservers, error).
func VerifyDelegation(ctx context.Context, zone string, expected []string) (bool, []string, error) {
	if len(expected) == 0 {
		return false, nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var lastErr error
	// Two independent resolvers: a delegation that only one of them sees is not
	// yet propagated.
	for _, resolver := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", resolver)
			},
		}
		nss, err := r.LookupNS(ctx, zone)
		if err != nil {
			lastErr = err
			continue
		}
		got := make([]string, 0, len(nss))
		for _, n := range nss {
			got = append(got, normalizeHost(n.Host))
		}
		sort.Strings(got)
		if intersects(got, expected) {
			return true, got, nil
		}
		return false, got, nil
	}
	return false, nil, lastErr
}

// intersects reports whether any expected nameserver appears in got. An exact
// set match is too strict — registrars and resolvers reorder, and some add their
// own — so agreement on at least one of ours is the useful signal.
func intersects(got, expected []string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, e := range expected {
		if set[normalizeHost(e)] {
			return true
		}
	}
	return false
}

// normalizeHost lowercases and strips the trailing dot DNS answers carry.
func normalizeHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}
