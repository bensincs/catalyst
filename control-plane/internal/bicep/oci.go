package bicep

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// Registry v2 HTTP API rather than Bicep's native `br:` restore. Bicep's restore
// is built for ACR (AAD auth) and the public MCR Bicep registry; everything else
// — notably GHCR — it can't pull, so we fetch those ourselves.
func ociHosted(ref string) bool {
	registry, _, _, err := splitModuleRef(ref)
	if err != nil {
		return false
	}
	host := strings.ToLower(registry)
	if strings.HasSuffix(host, ".azurecr.io") || host == "mcr.microsoft.com" {
		return false
	}
	return true
}

// fetchOCIModule pulls a published Bicep module's compiled ARM template from an
// OCI registry over the Registry v2 HTTP API. It handles the standard bearer
// challenge (anonymous by default; Basic creds from BICEP_OCI_USERNAME /
// BICEP_OCI_PASSWORD when the package is private). Returns the module's main.json.
func fetchOCIModule(ctx context.Context, ref string) ([]byte, error) {
	registry, repo, tag, err := splitModuleRef(ref)
	if err != nil {
		return nil, err
	}
	c := &ociClient{
		http:     &http.Client{Timeout: 30 * time.Second},
		scheme:   "https",
		registry: registry,
		user:     strings.TrimSpace(os.Getenv("BICEP_OCI_USERNAME")),
		pass:     strings.TrimSpace(os.Getenv("BICEP_OCI_PASSWORD")),
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
func (c *ociClient) authorize(ctx context.Context, challenge string) error {
	realm, params := parseChallenge(challenge)
	if realm == "" {
		return fmt.Errorf("registry %s: unauthorized and no bearer realm offered", c.registry)
	}
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
	if c.pass != "" {
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
