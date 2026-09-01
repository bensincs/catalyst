package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Pull access to the platform registry for a tenant's cluster.
//
// Private charts and Bicep modules are cached into the platform registry rather
// than pulled from their upstream by each tenant, so the upstream credential (a
// GHCR PAT, say) stays inside the platform — held in Key Vault and read only by
// the registry itself. What a tenant gets instead is a registry-scoped token:
//
//   - It is not an Entra principal, so it works for a delegated tenant whose
//     cluster lives in the customer's own directory. Granting a customer's
//     identity RBAC on a platform resource fails with PrincipalNotFound; this is
//     the same wall that ruled out a shared cluster and a platform-held DNS zone.
//   - It is scoped to the cached repositories only, so it cannot read the
//     control plane's own images in the same registry.
//   - It is per tenant, so revoking one tenant is deleting one token.

const acrAPIVersion = "2023-11-01-preview"

// registryScopeActions is the action list for one repository pattern. Pull needs
// the content; metadata/read is what lets a client resolve tags to digests.
func registryScopeActions(repos []string) []string {
	actions := make([]string, 0, len(repos)*2)
	for _, r := range repos {
		r = strings.Trim(strings.TrimSpace(r), "/")
		if r == "" {
			continue
		}
		actions = append(actions,
			"repositories/"+r+"/content/read",
			"repositories/"+r+"/metadata/read",
		)
	}
	return actions
}

// registryTokenName is the scope map / token name for a tenant. Registry object
// names allow only alphanumerics, dash and underscore.
func registryTokenName(slug string) string {
	var b strings.Builder
	b.WriteString("cortex-")
	for _, r := range strings.ToLower(slug) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EnsureTenantPullToken creates (or updates) the tenant's scope map and token on
// the platform registry and returns credentials for it. Returns empty strings
// when no registry is configured, which leaves the tenant pulling public charts
// anonymously — the previous behaviour.
func (p *Provisioner) EnsureTenantPullToken(ctx context.Context, slug string) (user, pass string, err error) {
	if strings.TrimSpace(p.platformACRID) == "" || len(p.platformACRRepos) == 0 {
		return "", "", nil
	}
	name := registryTokenName(slug)

	// Reuse the stored password when it still works. Generating one is
	// destructive — the registry hands a password back only by creating it,
	// which invalidates the previous — so minting on every call would break the
	// cluster already holding the old one, on every single sync.
	if u, pw, err := p.store.RegistryCredential(ctx, slug); err == nil && u != "" && pw != "" {
		if p.registryCredentialWorks(ctx, u, pw) {
			return u, pw, nil
		}
		slog.Info("infra: registry token rejected, re-minting", "tenant", slug)
	}
	base := "https://management.azure.com" + p.platformACRID

	scopeMapBody := map[string]any{
		"properties": map[string]any{
			"description": "Cortex tenant " + slug + " — pull cached charts and modules",
			"actions":     registryScopeActions(p.platformACRRepos),
		},
	}
	var scopeMap struct {
		ID string `json:"id"`
	}
	if err := p.armJSON(ctx, "PUT",
		fmt.Sprintf("%s/scopeMaps/%s?api-version=%s", base, name, acrAPIVersion),
		scopeMapBody, &scopeMap); err != nil {
		return "", "", fmt.Errorf("registry scope map: %w", err)
	}

	tokenBody := map[string]any{
		"properties": map[string]any{
			"scopeMapId": scopeMap.ID,
			"status":     "enabled",
		},
	}
	var token struct {
		ID string `json:"id"`
	}
	if err := p.armJSON(ctx, "PUT",
		fmt.Sprintf("%s/tokens/%s?api-version=%s", base, name, acrAPIVersion),
		tokenBody, &token); err != nil {
		return "", "", fmt.Errorf("registry token: %w", err)
	}

	// A token PUT answers 201 while the token is still being created, and
	// generating credentials against one that is not yet active fails with
	// 409 TokenNotActive — so the first provision of every tenant would fail
	// without waiting for it. Confirmed against the live API; it settles in
	// about five seconds.
	if err := p.awaitTokenActive(ctx, base, name); err != nil {
		return "", "", err
	}

	// Deliberately no expiry: a password that lapses would stop a tenant pulling
	// charts with nothing having changed, and the token is revoked by deleting
	// it rather than by waiting.
	var creds struct {
		Passwords []struct {
			Value string `json:"value"`
		} `json:"passwords"`
	}
	if err := p.armJSON(ctx, "POST",
		fmt.Sprintf("%s/generateCredentials?api-version=%s", base, acrAPIVersion),
		map[string]any{"tokenId": token.ID, "name": "password1"}, &creds); err != nil {
		return "", "", fmt.Errorf("registry credentials: %w", err)
	}
	if len(creds.Passwords) == 0 || creds.Passwords[0].Value == "" {
		return "", "", fmt.Errorf("registry credentials: none returned")
	}
	pass = creds.Passwords[0].Value
	if err := p.store.SetRegistryCredential(ctx, slug, name, pass); err != nil {
		// Not fatal for this call, but the next one would mint again and
		// invalidate what was just handed out, so it is worth shouting about.
		slog.Error("infra: storing registry token failed", "tenant", slug, "err", trunc(err.Error()))
	}
	return name, pass, nil
}

// registryCredentialWorks reports whether a stored token still authenticates.
// Cheap: an auth exchange against the registry, no pull.
func (p *Provisioner) registryCredentialWorks(ctx context.Context, user, pass string) bool {
	host := strings.TrimSpace(p.platformACRLogin)
	if host == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://%s/oauth2/token?service=%s&scope=registry:catalog:*", host, host), nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth(user, pass)
	resp, err := p.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// awaitTokenActive waits for a freshly written token to finish provisioning.
func (p *Provisioner) awaitTokenActive(ctx context.Context, base, name string) error {
	url := fmt.Sprintf("%s/tokens/%s?api-version=%s", base, name, acrAPIVersion)
	for attempt := range 20 {
		var tok struct {
			Properties struct {
				ProvisioningState string `json:"provisioningState"`
			} `json:"properties"`
		}
		if err := p.armJSON(ctx, "GET", url, nil, &tok); err != nil {
			return fmt.Errorf("registry token status: %w", err)
		}
		switch tok.Properties.ProvisioningState {
		case "Succeeded":
			return nil
		case "Failed", "Canceled":
			return fmt.Errorf("registry token %s: %s", name, tok.Properties.ProvisioningState)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	return fmt.Errorf("registry token %s did not become active", name)
}

// armJSON marshals body (when non-nil) and calls ARM, decoding into out.
func (p *Provisioner) armJSON(ctx context.Context, method, url string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}
	return p.arm(ctx, method, url, raw, out)
}

// DeleteTenantPullToken removes a tenant's registry access. Best-effort: it runs
// during teardown, where a missing token is the desired end state anyway.
func (p *Provisioner) DeleteTenantPullToken(ctx context.Context, slug string) {
	if strings.TrimSpace(p.platformACRID) == "" {
		return
	}
	name := registryTokenName(slug)
	base := "https://management.azure.com" + p.platformACRID
	// The token references the scope map, so it has to go first.
	_ = p.armDelete(ctx, fmt.Sprintf("%s/tokens/%s?api-version=%s", base, name, acrAPIVersion))
	_ = p.armDelete(ctx, fmt.Sprintf("%s/scopeMaps/%s?api-version=%s", base, name, acrAPIVersion))
}
