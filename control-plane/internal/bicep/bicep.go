// Package bicep resolves a deployment's Bicep infra — provided as an OCI module
// reference (e.g. br:myacr.azurecr.io/bicep/db:1.0.0) — into an ARM template plus
// its output names at author time, so the reconciler can provision it directly
// without a Bicep toolchain. It shells out to the `bicep` CLI (or `az bicep`).
package bicep

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
)

// ErrNoCompiler is returned when neither `bicep` nor `az bicep` is on PATH.
var ErrNoCompiler = errors.New("no bicep compiler available")

// ErrBadRef is returned when the reference is neither an OCI module ref nor an
// inline ARM JSON template.
var ErrBadRef = errors.New("not an OCI Bicep module reference (br:… / oci://…) or an ARM template")

// Resolve turns an infra reference into a deployable ARM template + the names of
// its outputs (for wiring). The reference is either an OCI Bicep module
// (br:registry/repo:tag or oci://…) or an inline ARM JSON template. Empty input
// yields ("", nil, nil). Returns ErrNoCompiler when a module ref needs a
// toolchain that isn't present, or a build error (with the compiler message) for
// an invalid module.
func Resolve(ctx context.Context, ref string) (arm string, outputs []string, err error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return "", nil, nil
	}
	if strings.HasPrefix(s, "{") { // already an ARM template
		return s, armOutputNames(s), nil
	}
	if !isModuleRef(s) {
		return "", nil, ErrBadRef
	}
	if !Available() {
		return "", nil, ErrNoCompiler
	}
	dir, err := os.MkdirTemp("", "cortex-bicep-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)

	// Pass 1: build a wrapper that references the module, to discover its outputs.
	arm1, err := build(ctx, dir, wrapper(s, nil))
	if err != nil {
		return "", nil, err
	}
	outs := moduleOutputTypes(arm1)
	if len(outs) == 0 {
		return arm1, nil, nil // module has no outputs — nothing to wire
	}
	// Pass 2: re-export the module's outputs at the top level so the deployment
	// surfaces them (that's what the reconciler reads + wires).
	arm2, err := build(ctx, dir, wrapper(s, outs))
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(outs))
	for k := range outs {
		names = append(names, k)
	}
	sort.Strings(names)
	return arm2, names, nil
}

// Available reports whether a Bicep toolchain is on PATH.
func Available() bool {
	if _, err := exec.LookPath("bicep"); err == nil {
		return true
	}
	_, err := exec.LookPath("az")
	return err == nil
}

func isModuleRef(s string) bool {
	return strings.HasPrefix(s, "br:") || strings.HasPrefix(s, "br/") || strings.HasPrefix(s, "oci://")
}

// wrapper generates a Bicep file that instantiates the OCI module and re-exports
// the given outputs (name → Bicep type).
func wrapper(ref string, outputs map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "module infra '%s' = {\n  name: 'infra'\n}\n", ref)
	names := make([]string, 0, len(outputs))
	for k := range outputs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		t := outputs[name]
		if t == "" {
			t = "string"
		}
		fmt.Fprintf(&b, "output %s %s = infra.outputs.%s\n", name, t, name)
	}
	return b.String()
}

// moduleOutputTypes reads the nested module's outputs (name → Bicep type) from a
// compiled wrapper ARM template (the module becomes a nested deployment).
func moduleOutputTypes(arm string) map[string]string {
	var t struct {
		Resources []struct {
			Type       string `json:"type"`
			Properties struct {
				Template struct {
					Outputs map[string]struct {
						Type string `json:"type"`
					} `json:"outputs"`
				} `json:"template"`
			} `json:"properties"`
		} `json:"resources"`
	}
	if json.Unmarshal([]byte(arm), &t) != nil {
		return nil
	}
	for _, r := range t.Resources {
		if strings.EqualFold(r.Type, "Microsoft.Resources/deployments") && len(r.Properties.Template.Outputs) > 0 {
			out := make(map[string]string, len(r.Properties.Template.Outputs))
			for k, v := range r.Properties.Template.Outputs {
				out[k] = armTypeToBicep(v.Type)
			}
			return out
		}
	}
	return nil
}

// armOutputNames reads top-level output names from an inline ARM template.
func armOutputNames(arm string) []string {
	var t struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	if json.Unmarshal([]byte(arm), &t) != nil {
		return nil
	}
	names := make([]string, 0, len(t.Outputs))
	for k := range t.Outputs {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func armTypeToBicep(t string) string {
	switch strings.ToLower(t) {
	case "int":
		return "int"
	case "bool":
		return "bool"
	case "array":
		return "array"
	case "object", "securestring", "secureobject":
		if strings.EqualFold(t, "array") {
			return "array"
		}
		if strings.HasPrefix(strings.ToLower(t), "sec") {
			return "string"
		}
		return "object"
	default:
		return "string"
	}
}

func build(ctx context.Context, dir, source string) (string, error) {
	in := filepath.Join(dir, "main.bicep")
	out := filepath.Join(dir, "main.json")
	if err := os.WriteFile(in, []byte(source), 0o600); err != nil {
		return "", err
	}
	cmd, err := compileCmd(ctx, in, out)
	if err != nil {
		return "", err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("bicep build failed: %s", strings.TrimSpace(string(b)))
	}
	arm, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	return string(arm), nil
}

func compileCmd(ctx context.Context, in, out string) (*exec.Cmd, error) {
	if p, err := exec.LookPath("bicep"); err == nil {
		return exec.CommandContext(ctx, p, "build", in, "--outfile", out), nil
	}
	if p, err := exec.LookPath("az"); err == nil {
		return exec.CommandContext(ctx, p, "bicep", "build", "--file", in, "--outfile", out), nil
	}
	return nil, ErrNoCompiler
}
