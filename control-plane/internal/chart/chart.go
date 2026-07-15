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
	"strings"

	"sigs.k8s.io/yaml"
)

// ErrNoHelm is returned when the helm CLI isn't on PATH.
var ErrNoHelm = errors.New("no helm CLI available")

// ErrBadRef is returned when neither an HTTP repo+chart nor an OCI ref is given.
var ErrBadRef = errors.New("a Helm repo + chart (or an oci:// reference) is required")

// Interface is a chart's authoring surface for the values builder.
type Interface struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Defaults    json.RawMessage `json:"defaults"`         // values.yaml → JSON (the value tree + defaults)
	Schema      json.RawMessage `json:"schema,omitempty"` // values.schema.json (JSON Schema), when present
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
func Inspect(ctx context.Context, repoURL, chart, version string) (*Interface, error) {
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
	return buildInterface(v, s, c), nil
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
