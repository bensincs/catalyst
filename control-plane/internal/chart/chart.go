// Package chart inspects a Helm chart's authoring surface — its default values
// (values.yaml) and optional JSON Schema (values.schema.json) — so the console
// can render a typed values builder instead of a raw YAML textarea. It shells out
// to the `helm` CLI (pinned in the control-plane image), mirroring internal/bicep.
package chart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// ErrNoHelm is returned when the helm CLI isn't on PATH.
var ErrNoHelm = errors.New("no helm CLI available")

// ErrBadRef is returned when neither an HTTP repo+chart nor an OCI ref is given.
var ErrBadRef = errors.New("a Helm repo + chart (or an oci:// reference) is required")

// Service is a Service the chart renders — an exposure candidate. The name is
// the real object name for the release being authored, not a template, because
// the reconciler routes to it verbatim: a name that is merely close does not
// resolve, and the app silently serves nothing.
type Service struct {
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

// Interface is a chart's authoring surface for the values builder.
type Interface struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Defaults    json.RawMessage `json:"defaults"`         // values.yaml → JSON (the value tree + defaults)
	Schema      json.RawMessage `json:"schema,omitempty"` // values.schema.json (JSON Schema), when present
	Services    []Service       `json:"services,omitempty"`
}

// Available reports whether the helm CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath("helm")
	return err == nil
}

// Inspect pulls a chart and reads its values interface. repoURL is an HTTP Helm
// repo or an oci:// registry; chart is the chart name; version pins it (empty =
// latest). Returns ErrNoHelm when the toolchain is absent (the console then falls
// back to a raw YAML editor).
func Inspect(ctx context.Context, repoURL, chart, version, release, values string) (*Interface, error) {
	repoURL, chart = strings.TrimSpace(repoURL), strings.TrimSpace(chart)
	oci := strings.HasPrefix(repoURL, "oci://")
	if (!oci && (repoURL == "" || chart == "")) || (oci && repoURL == "") {
		return nil, ErrBadRef
	}
	if !Available() {
		return nil, ErrNoHelm
	}
	dir, err := os.MkdirTemp("", "cortex-chart-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	args := []string{"pull"}
	// Credentials for the platform registry. Charts on a private upstream are
	// cached into it rather than pulled from the upstream directly, so this is
	// the only registry the control plane ever needs to authenticate against —
	// the upstream's own credential stays in the registry's credential set.
	if u, pw := registryCreds(repoURL); u != "" {
		args = append(args, "--username", u, "--password", pw)
	}
	if oci {
		ref := repoURL
		if chart != "" {
			ref = strings.TrimSuffix(repoURL, "/") + "/" + chart
		}
		args = append(args, ref)
	} else {
		args = append(args, chart, "--repo", repoURL)
	}
	if version != "" {
		args = append(args, "--version", version)
	}
	args = append(args, "--untar", "--untardir", dir)

	if out, err := exec.CommandContext(ctx, "helm", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("helm pull failed: %s", trunc(strings.TrimSpace(string(out))))
	}
	chartDir, err := singleSubdir(dir)
	if err != nil {
		return nil, err
	}
	v, _ := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	s, _ := os.ReadFile(filepath.Join(chartDir, "values.schema.json"))
	c, _ := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	iface := buildInterface(v, s, c)
	iface.Services = renderServices(ctx, chartDir, dir, release, values)
	return iface, nil
}

// renderServices templates the chart and reports the Services it produces.
//
// Rendering is best-effort: a chart may need cluster capabilities it cannot have
// here, and half-written values are normal while the author is still typing.
// Either way the console falls back to naming the service by hand, so a failure
// must never fail the inspection.
func renderServices(ctx context.Context, chartDir, tmpDir, release, values string) []Service {
	if strings.TrimSpace(release) == "" {
		return nil
	}
	render := func(withValues bool) ([]byte, error) {
		args := []string{"template", release, chartDir}
		if withValues {
			f := filepath.Join(tmpDir, "values-in.yaml")
			if err := os.WriteFile(f, []byte(values), 0o600); err != nil {
				return nil, err
			}
			args = append(args, "--values", f)
		}
		return exec.CommandContext(ctx, "helm", args...).Output()
	}

	out, err := render(strings.TrimSpace(values) != "")
	if err != nil {
		// The author's values are the usual reason rendering fails mid-edit.
		// Fall back to the chart's own defaults so the list still appears; the
		// names only shift if a value actually renames a Service.
		if out, err = render(false); err != nil {
			return nil
		}
	}
	return servicesFromManifests(out)
}

// servicesFromManifests picks the Services out of a rendered multi-document
// manifest stream (pure, so it unit-tests without helm).
func servicesFromManifests(manifests []byte) []Service {
	var out []Service
	seen := map[string]bool{}
	for _, doc := range strings.Split(string(manifests), "\n---") {
		if !strings.Contains(doc, "kind: Service") {
			continue
		}
		var m struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type  string `json:"type"`
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		}
		j, err := yaml.YAMLToJSON([]byte(doc))
		if err != nil || len(j) == 0 {
			continue
		}
		if err := json.Unmarshal(j, &m); err != nil {
			continue
		}
		// "ServiceAccount" also contains the substring, so match the kind exactly.
		if m.Kind != "Service" || m.Metadata.Name == "" || seen[m.Metadata.Name] {
			continue
		}
		seen[m.Metadata.Name] = true
		svc := Service{Name: m.Metadata.Name, Type: m.Spec.Type}
		for _, p := range m.Spec.Ports {
			if p.Port > 0 {
				svc.Ports = append(svc.Ports, p.Port)
			}
		}
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildInterface assembles the Interface from the three chart files (pure, so it
// unit-tests without helm).
func buildInterface(valuesYAML, schemaJSON, chartYAML []byte) *Interface {
	iface := &Interface{Defaults: json.RawMessage("{}")}
	if len(valuesYAML) > 0 {
		if j, err := yaml.YAMLToJSON(valuesYAML); err == nil && len(j) > 0 && string(j) != "null" {
			iface.Defaults = j
		}
	}
	if len(schemaJSON) > 0 && json.Valid(schemaJSON) {
		iface.Schema = json.RawMessage(schemaJSON)
	}
	if len(chartYAML) > 0 {
		var meta struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
		}
		if j, err := yaml.YAMLToJSON(chartYAML); err == nil {
			_ = json.Unmarshal(j, &meta)
		}
		iface.Name, iface.Version, iface.Description = meta.Name, meta.Version, meta.Description
	}
	return iface
}

// registryCreds returns the credentials to use for repoURL, which are only ever
// the platform registry's: HELM_OCI_REGISTRY names it, and a ref pointing
// anywhere else (a public chart repo, say) is fetched anonymously. Mirrors the
// BICEP_OCI_* pair used for Bicep modules.
func registryCreds(repoURL string) (user, pass string) {
	reg := strings.TrimSpace(os.Getenv("HELM_OCI_REGISTRY"))
	if reg == "" {
		return "", ""
	}
	// Compare on host, so oci:// and a bare host both match.
	host := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(repoURL), "oci://"), "https://")
	reg = strings.TrimPrefix(strings.TrimPrefix(reg, "oci://"), "https://")
	if h, _, ok := strings.Cut(host, "/"); ok {
		host = h
	}
	if r, _, ok := strings.Cut(reg, "/"); ok {
		reg = r
	}
	if !strings.EqualFold(host, reg) {
		return "", ""
	}
	return strings.TrimSpace(os.Getenv("HELM_OCI_USERNAME")), os.Getenv("HELM_OCI_PASSWORD")
}

func singleSubdir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no chart directory extracted")
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
