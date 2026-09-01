package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"
)

// vaultScope is the Key Vault data-plane audience.
const vaultScope = "https://vault.azure.net/.default"

// Upstream registries cached into the platform registry, managed at runtime.
//
// A platform admin adds these from the console rather than through a redeploy,
// so the registry itself is the source of truth — nothing is mirrored into the
// database, and there is no second copy to drift from what Azure actually has.
//
// A private upstream needs a credential set, which ACR reads from Key Vault. The
// credential therefore lands in the vault and is read by the registry; it is
// never handed to a tenant, which pulls the cached copy using its own scoped
// token instead.

// Upstream is one cached upstream repository.
type Upstream struct {
	Name         string `json:"name"`
	Source       string `json:"source"`      // e.g. ghcr.io/acme/charts/*
	Target       string `json:"target"`      // e.g. charts/*
	LoginServer  string `json:"loginServer"` // upstream host, derived from Source
	Credentialed bool   `json:"credentialed"`
	State        string `json:"state,omitempty"`
}

// upstreamHost reduces a source pattern to its registry host.
func upstreamHost(source string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(source), "oci://"), "https://")
	if before, _, ok := strings.Cut(h, "/"); ok {
		h = before
	}
	return h
}

// credentialSetName is the per-host credential set name. A credential set is
// bound to one login server, so hosts get one each.
func credentialSetName(host string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(host) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// ListUpstreams reports the cache rules configured on the platform registry.
func (p *Provisioner) ListUpstreams(ctx context.Context) ([]Upstream, error) {
	if strings.TrimSpace(p.platformACRID) == "" {
		return []Upstream{}, nil
	}
	var page struct {
		Value []struct {
			Name       string `json:"name"`
			Properties struct {
				SourceRepository        string `json:"sourceRepository"`
				TargetRepository        string `json:"targetRepository"`
				CredentialSetResourceID string `json:"credentialSetResourceId"`
				ProvisioningState       string `json:"provisioningState"`
			} `json:"properties"`
		} `json:"value"`
	}
	url := fmt.Sprintf("https://management.azure.com%s/cacheRules?api-version=%s", p.platformACRID, acrAPIVersion)
	if err := p.armJSON(ctx, "GET", url, nil, &page); err != nil {
		return nil, err
	}
	out := make([]Upstream, 0, len(page.Value))
	for _, r := range page.Value {
		out = append(out, Upstream{
			Name:         r.Name,
			Source:       r.Properties.SourceRepository,
			Target:       r.Properties.TargetRepository,
			LoginServer:  upstreamHost(r.Properties.SourceRepository),
			Credentialed: strings.TrimSpace(r.Properties.CredentialSetResourceID) != "",
			State:        r.Properties.ProvisioningState,
		})
	}
	return out, nil
}

// PutUpstream creates or updates a cache rule. When a username and password are
// given the upstream is private: the credential is written to Key Vault and a
// credential set for that host is pointed at it. Passing an empty password on an
// existing host keeps whatever credential is already there, so an admin editing
// a rule does not have to re-enter the token.
func (p *Provisioner) PutUpstream(ctx context.Context, name, source, target, user, pass string) error {
	if strings.TrimSpace(p.platformACRID) == "" {
		return fmt.Errorf("no platform registry configured")
	}
	name, source, target = strings.TrimSpace(name), strings.TrimSpace(source), strings.TrimSpace(target)
	if name == "" || source == "" || target == "" {
		return fmt.Errorf("name, source and target are required")
	}
	base := "https://management.azure.com" + p.platformACRID
	props := map[string]any{"sourceRepository": source, "targetRepository": target}

	host := upstreamHost(source)
	if strings.TrimSpace(user) != "" && strings.TrimSpace(pass) != "" {
		id, err := p.ensureCredentialSet(ctx, host, user, pass)
		if err != nil {
			return err
		}
		props["credentialSetResourceId"] = id
	} else if existing, err := p.credentialSetID(ctx, host); err == nil && existing != "" {
		// Keep an already-configured credential rather than silently making the
		// rule anonymous, which would break pulls the next time the cache misses.
		props["credentialSetResourceId"] = existing
	}

	return p.armJSON(ctx, "PUT",
		fmt.Sprintf("%s/cacheRules/%s?api-version=%s", base, name, acrAPIVersion),
		map[string]any{"properties": props}, nil)
}

// DeleteUpstream removes a cache rule. The cached artifacts and the credential
// set are left alone: other rules may share the credential, and deleting the
// cached copies would break tenants still pulling them.
func (p *Provisioner) DeleteUpstream(ctx context.Context, name string) error {
	if strings.TrimSpace(p.platformACRID) == "" {
		return fmt.Errorf("no platform registry configured")
	}
	return p.armDelete(ctx, fmt.Sprintf("https://management.azure.com%s/cacheRules/%s?api-version=%s",
		p.platformACRID, strings.TrimSpace(name), acrAPIVersion))
}

// credentialSetID returns the resource id of the credential set for a host, or
// empty when there isn't one.
func (p *Provisioner) credentialSetID(ctx context.Context, host string) (string, error) {
	var cs struct {
		ID string `json:"id"`
	}
	err := p.armJSON(ctx, "GET",
		fmt.Sprintf("https://management.azure.com%s/credentialSets/%s?api-version=%s",
			p.platformACRID, credentialSetName(host), acrAPIVersion), nil, &cs)
	if err != nil {
		return "", err
	}
	return cs.ID, nil
}

// ensureCredentialSet writes the upstream credential to Key Vault and points a
// credential set for that host at it.
func (p *Provisioner) ensureCredentialSet(ctx context.Context, host, user, pass string) (string, error) {
	if strings.TrimSpace(p.keyVaultURI) == "" {
		return "", fmt.Errorf("no key vault configured for upstream credentials")
	}
	csName := credentialSetName(host)
	userURI, err := p.putVaultSecret(ctx, "upstream-"+csName+"-username", user)
	if err != nil {
		return "", err
	}
	passURI, err := p.putVaultSecret(ctx, "upstream-"+csName+"-password", pass)
	if err != nil {
		return "", err
	}

	var cs struct {
		ID       string `json:"id"`
		Identity struct {
			PrincipalID string `json:"principalId"`
		} `json:"identity"`
	}
	body := map[string]any{
		"identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{
			"loginServer": host,
			"authCredentials": []any{map[string]any{
				"name":                     "Credential1",
				"usernameSecretIdentifier": userURI,
				"passwordSecretIdentifier": passURI,
			}},
		},
	}
	if err := p.armJSON(ctx, "PUT",
		fmt.Sprintf("https://management.azure.com%s/credentialSets/%s?api-version=%s",
			p.platformACRID, csName, acrAPIVersion), body, &cs); err != nil {
		return "", fmt.Errorf("credential set: %w", err)
	}
	// The credential set reads the vault as its own identity, which only exists
	// once the set does — so the grant cannot be made ahead of time.
	if cs.Identity.PrincipalID != "" {
		if err := p.grantVaultRead(ctx, cs.Identity.PrincipalID); err != nil {
			return "", err
		}
	}
	return cs.ID, nil
}

// putVaultSecret writes a secret to the platform vault over its data plane and
// returns the versionless identifier, so rotating the secret does not require
// rewriting the credential set that points at it.
func (p *Provisioner) putVaultSecret(ctx context.Context, name, value string) (string, error) {
	tok, err := p.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{vaultScope}})
	if err != nil {
		return "", fmt.Errorf("acquire vault token: %w", err)
	}
	body, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		return "", err
	}
	uri := strings.TrimSuffix(p.keyVaultURI, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/secrets/%s?api-version=7.4", uri, name), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("vault secret %s: %d %s", name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return fmt.Sprintf("%s/secrets/%s", uri, name), nil
}

// grantVaultRead lets a principal read the vault's secrets. Idempotent: an
// existing assignment answers 409, which is the desired state.
func (p *Provisioner) grantVaultRead(ctx context.Context, principalID string) error {
	if strings.TrimSpace(p.keyVaultID) == "" {
		return nil
	}
	// Key Vault Secrets User.
	const roleID = "4633458b-17de-408a-b874-0445c86b69e6"
	sub, _, _, ok := parseResourceID(p.keyVaultID)
	if !ok {
		return fmt.Errorf("unparseable key vault id")
	}
	name := uuid.NewString()
	body := map[string]any{"properties": map[string]any{
		"roleDefinitionId": fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", sub, roleID),
		"principalId":      principalID,
		"principalType":    "ServicePrincipal",
	}}
	err := p.armJSON(ctx, "PUT", fmt.Sprintf(
		"https://management.azure.com%s/providers/Microsoft.Authorization/roleAssignments/%s?api-version=2022-04-01",
		p.keyVaultID, name), body, nil)
	if err != nil && strings.Contains(err.Error(), "RoleAssignmentExists") {
		return nil
	}
	return err
}
