package bicep

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// bicepModuleLayerMediaType is the OCI layer media type a published Bicep module
// carries — its single layer is the module's compiled ARM template (main.json).
const bicepModuleLayerMediaType = "application/vnd.ms.bicep.module.layer.v1+json"

// ociImageManifestMediaType is the manifest a `bicep publish` artifact uses.
const ociImageManifestMediaType = "application/vnd.oci.image.manifest.v1+json"

// localModuleFile is the on-disk name we give a pulled module so a wrapper can
// reference it as a local (compiled) module instead of an OCI ref.
const localModuleFile = "module.json"

// ociHosted reports whether a module ref must be fetched over the plain OCI
// Registry v2 HTTP API rather than Bicep's native `br:` restore.
//
// Bicep's restore authenticates to ACR with AAD, which requires an Azure
// credential the Bicep CLI can find. In the control plane's container there is
// none — the process holds a managed identity, but the CLI has no way to use it,
// and a restore fails with CredentialUnavailableException. What the control
// plane does hold is a registry TOKEN for the platform ACR, which the plain
// Registry v2 API accepts. So the platform registry is fetched here too, and
// only the public MCR registry (which needs no credential at all) is left to the
// CLI. Everything else — notably GHCR — the CLI cannot pull regardless.
func ociHosted(ref string) bool {
	registry, _, _, err := splitModuleRef(ref)
	if err != nil {
		return false
	}
	host := strings.ToLower(registry)
	if host == "mcr.microsoft.com" {
		return false
	}
	// The registry we hold a token for: fetch it ourselves rather than asking
	// the CLI to find an Azure credential it does not have.
	if cr := credRegistry(); cr != "" && strings.EqualFold(hostOnly(cr), hostOnly(host)) {
		return true
	}
	// Any other ACR: the CLI's AAD restore is the only option we have.
	return !strings.HasSuffix(host, ".azurecr.io")
}

// credRegistry is the ONE registry the configured credential belongs to.
//
// This exists because the credential used to be sent to whatever registry the
// author named, and the author names it. Two things went wrong with that:
// pointing an infra entity at a registry the credential is not for produced a
// confusing 403 (an ACR token offered to ghcr.io), and — far worse — pointing it
// at a registry an attacker controls handed them the platform's registry
// credential. A credential now travels only to the host it was issued for.
func credRegistry() string {
	if v := strings.TrimSpace(os.Getenv("BICEP_OCI_REGISTRY")); v != "" {
		return v
	}
	// Falls back to the platform registry, which is what the credential is for.
	return strings.TrimSpace(os.Getenv("HELM_OCI_REGISTRY"))
}

// fetchOCIModule pulls a published Bicep module's compiled ARM template from an
// OCI registry over the Registry v2 HTTP API. It handles the standard bearer
// challenge, anonymously unless the registry is the one the configured
// credential belongs to. Returns the module's main.json.
func fetchOCIModule(ctx context.Context, ref string) ([]byte, error) {
	registry, repo, tag, err := splitModuleRef(ref)
	if err != nil {
		return nil, err
	}
	// The host is caller-supplied and becomes an outbound request from a process
	// with a managed identity inside a private network.
	if err := checkPublicHost(registry); err != nil {
		return nil, err
	}
	c := &ociClient{
		http:     &http.Client{Timeout: 30 * time.Second},
		scheme:   "https",
		registry: registry,
	}
	// Credentials only for the registry they were issued for.
	if cr := credRegistry(); cr != "" && strings.EqualFold(cr, registry) {
		c.user = strings.TrimSpace(os.Getenv("BICEP_OCI_USERNAME"))
		c.pass = strings.TrimSpace(os.Getenv("BICEP_OCI_PASSWORD"))
	}
	man, err := c.manifest(ctx, repo, tag)
	if err != nil {
		return nil, err
	}
	digest := ""
	for _, l := range man.Layers {
		if l.MediaType == bicepModuleLayerMediaType {
			digest = l.Digest
			break
		}
	}
	// Fall back to the sole layer for a single-layer artifact whose media type
	// differs (e.g. published by a tool that didn't set the Bicep layer type).
	if digest == "" && len(man.Layers) == 1 {
		digest = man.Layers[0].Digest
	}
	if digest == "" {
		return nil, fmt.Errorf("no Bicep module layer in %s", ref)
	}
	return c.blob(ctx, repo, digest)
}

// credentialsAllowedFor reports whether the configured Basic credential may be
// presented to a token endpoint on realmHost.
//
// Only the registry's own host qualifies. The realm is supplied by the registry,
// and the registry is named by whoever authored the module reference, so any
// other answer hands a chosen third party the platform's registry credential.
// The port is ignored: a registry on a non-default port and its token endpoint
// on 443 are the same operator.
func (c *ociClient) credentialsAllowedFor(realmHost string) bool {
	return strings.EqualFold(hostOnly(realmHost), hostOnly(c.registry))
}

// hostOnly strips a port so "reg:443" and "reg" compare equal.
func hostOnly(hostPort string) string {
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return hostPort
}

type ociManifest struct {
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

// ociClient is a minimal OCI Registry v2 client: fetch a manifest + a blob,
// acquiring a bearer token on demand from the registry's auth challenge.
type ociClient struct {
	http       *http.Client
	scheme     string // "https" in prod; "http" for tests
	registry   string
	user, pass string
	token      string // cached bearer, populated on the first 401 challenge
}

func (c *ociClient) manifest(ctx context.Context, repo, tag string) (ociManifest, error) {
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", c.scheme, c.registry, repo, tag)
	resp, err := c.get(ctx, url, ociImageManifestMediaType)
	if err != nil {
		return ociManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ociManifest{}, fmt.Errorf("fetch manifest %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var m ociManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return ociManifest{}, err
	}
	return m, nil
}

func (c *ociClient) blob(ctx context.Context, repo, digest string) ([]byte, error) {
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", c.scheme, c.registry, repo, digest)
	resp, err := c.get(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fetch blob %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// get issues a GET, transparently answering a 401 bearer challenge once and
// retrying with the acquired token.
func (c *ociClient) get(ctx context.Context, url, accept string) (*http.Response, error) {
	resp, err := c.rawGet(ctx, url, accept)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("Www-Authenticate")
		resp.Body.Close()
		if err := c.authorize(ctx, challenge); err != nil {
			return nil, err
		}
		return c.rawGet(ctx, url, accept)
	}
	return resp, nil
}

func (c *ociClient) rawGet(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.http.Do(req)
}

// authorize follows a Bearer challenge (realm/service/scope) to fetch a token,
// sending Basic creds when configured (private packages) or anonymously.
//
// The realm comes from the registry's own 401 response, so on a registry the
// author chose it is attacker-controlled. Two guards follow from that, and
// neither is optional:
//
//   - The realm host is checked like any other caller-supplied host, because
//     otherwise a registry could point it at 169.254.169.254 and have this
//     process fetch instance metadata for it.
//   - Credentials are sent only when the realm is on the registry's own host. A
//     registry that answers with realm="https://attacker.example/token" would
//     otherwise be handed the platform's registry credential in a Basic header.
func (c *ociClient) authorize(ctx context.Context, challenge string) error {
	realm, params := parseChallenge(challenge)
	if realm == "" {
		return fmt.Errorf("registry %s: unauthorized and no bearer realm offered", c.registry)
	}
	realmURL, err := url.Parse(realm)
	if err != nil || realmURL.Host == "" {
		return fmt.Errorf("registry %s: unusable bearer realm", c.registry)
	}
	// Must match the scheme we are talking to the registry on: https in
	// production, and http only because the tests run a local registry.
	if realmURL.Scheme != c.scheme {
		return fmt.Errorf("registry %s: bearer realm is not %s", c.registry, c.scheme)
	}
	// Production always talks https; the scheme check above already confines
	// this to the tests' local registry, which is necessarily not routable.
	if c.scheme == "https" {
		if err := checkPublicHost(realmURL.Host); err != nil {
			return fmt.Errorf("registry %s: bearer realm %s", c.registry, err)
		}
	}
	sendCreds := c.credentialsAllowedFor(realmURL.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	if s := params["scope"]; s != "" {
		q.Set("scope", s)
	}
	req.URL.RawQuery = q.Encode()
	if c.pass != "" && sendCreds {
		user := c.user
		if user == "" {
			user = "x" // GHCR accepts any username with a PAT as the password
		}
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+c.pass)))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("token fetch %s: %d %s", realm, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	if t.Token != "" {
		c.token = t.Token
	} else {
		c.token = t.AccessToken
	}
	if c.token == "" {
		return fmt.Errorf("token fetch %s: empty token", realm)
	}
	return nil
}

// parseChallenge extracts the realm + key/value params from a WWW-Authenticate
// Bearer header, e.g. Bearer realm="https://ghcr.io/token",service="ghcr.io",…
func parseChallenge(h string) (realm string, params map[string]string) {
	params = map[string]string{}
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return "", params
	}
	for _, part := range splitCSVOutsideQuotes(h[len("bearer "):]) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		if strings.EqualFold(k, "realm") {
			realm = v
		} else {
			params[strings.ToLower(k)] = v
		}
	}
	return realm, params
}

// splitCSVOutsideQuotes splits on commas that aren't inside a quoted value (a
// scope value can itself contain commas).
func splitCSVOutsideQuotes(s string) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
