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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ErrNoCompiler is returned when neither `bicep` nor `az bicep` is on PATH.
var ErrNoCompiler = errors.New("no bicep compiler available")

// ErrBadRef is returned when the reference is neither an OCI module ref nor an
// inline ARM JSON template.
var ErrBadRef = errors.New("not an OCI Bicep module reference (br:… / oci://…) or an ARM template")

// Resolve turns an infra reference into a deployable ARM template + the names of
// its outputs (for wiring). The reference is either an OCI Bicep module
// (br:registry/repo:tag or oci://…) or an inline ARM JSON template. params are
// the module's input parameters (author-supplied); they're baked into the
// resolved template as literals, so the reconciler deploys it without needing to
// pass parameters. Empty input yields ("", nil, nil). Returns ErrNoCompiler when
// a module ref needs a toolchain that isn't present, or a build error (with the
// compiler message — e.g. which required params are missing) for an invalid
// module.
func Resolve(ctx context.Context, ref string, params map[string]any) (arm string, outputs []string, err error) {
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
	arm1, err := build(ctx, dir, wrapper(s, nil, params))
	if err != nil {
		return "", nil, err
	}
	outs := moduleOutputTypes(arm1)
	if len(outs) == 0 {
		return arm1, nil, nil // module has no outputs — nothing to wire
	}
	// Pass 2: re-export the module's outputs at the top level so the deployment
	// surfaces them (that's what the reconciler reads + wires).
	arm2, err := build(ctx, dir, wrapper(s, outs, params))
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

// wrapper generates a Bicep file that instantiates the OCI module with the
// author's params and re-exports the given outputs (name → Bicep type).
func wrapper(ref string, outputs map[string]string, params map[string]any) string {
	var b strings.Builder
	// Unique nested-deployment name per infra: each Bicep module compiles to a
	// nested Microsoft.Resources/deployments, and two infra entities deploying into
	// the same resource group must not collide on a shared 'infra' name. deployment()
	// is the outer deployment (cortex-app-<id>, unique per infra), resolved at deploy.
	fmt.Fprintf(&b, "module infra '%s' = {\n  name: '${deployment().name}-m'\n", ref)
	if len(params) > 0 {
		fmt.Fprintf(&b, "  params: %s\n", bicepObject(params, 1))
	}
	b.WriteString("}\n")
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
		// Safe re-export: AVM outputs are frequently nullable (their value is absent
		// when an optional resource isn't deployed — e.g. systemAssignedMIPrincipalId
		// with no system identity). A raw `.value` re-export then fails the whole
		// deployment with DeploymentOutputEvaluationFailed. Safe-access (.?) + coalesce
		// to a zero value keeps the deployment — and every other output — valid.
		fmt.Fprintf(&b, "output %s %s = infra.outputs.?%s ?? %s\n", name, t, name, zeroValue(t))
	}
	return b.String()
}

// zeroValue is the Bicep zero literal for a re-exported output's type, used to
// coalesce a nullable/absent module output to a concrete value.
func zeroValue(bicepType string) string {
	switch bicepType {
	case "int":
		return "0"
	case "bool":
		return "false"
	case "array":
		return "[]"
	case "object":
		return "{}"
	default:
		return "''"
	}
}

var bicepIdent = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// bicepObject renders a JSON-decoded map as a Bicep object literal. indent is the
// nesting depth (2 spaces per level); the closing brace sits at that depth and
// the entries one deeper, so the block aligns under `params:`.
func bicepObject(m map[string]any, indent int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pad := strings.Repeat("  ", indent+1)
	end := strings.Repeat("  ", indent)
	var b strings.Builder
	b.WriteString("{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s%s: %s\n", pad, bicepKey(k), bicepValue(m[k], indent+1))
	}
	b.WriteString(end + "}")
	return b.String()
}

func bicepArray(a []any, indent int) string {
	if len(a) == 0 {
		return "[]"
	}
	pad := strings.Repeat("  ", indent+1)
	end := strings.Repeat("  ", indent)
	var b strings.Builder
	b.WriteString("[\n")
	for _, item := range a {
		fmt.Fprintf(&b, "%s%s\n", pad, bicepValue(item, indent+1))
	}
	b.WriteString(end + "]")
	return b.String()
}

// bicepValue renders a single JSON-decoded value as a Bicep literal. Bicep has no
// float type, so whole-number floats collapse to ints; non-integral numbers are
// emitted as-is (the compiler rejects them, surfacing a clear author error).
func bicepValue(v any, indent int) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case string:
		return "'" + escapeBicepString(x) + "'"
	case json.Number:
		return x.String()
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any:
		return bicepArray(x, indent)
	case map[string]any:
		return bicepObject(x, indent)
	default:
		b, _ := json.Marshal(x)
		return "'" + escapeBicepString(string(b)) + "'"
	}
}

func bicepKey(k string) string {
	if bicepIdent.MatchString(k) {
		return k
	}
	return "'" + escapeBicepString(k) + "'"
}

// escapeBicepString escapes a Go string for a single-quoted Bicep string literal.
func escapeBicepString(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`$`, `\$`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return r.Replace(s)
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
		return "", fmt.Errorf("%s", cleanBicepError(string(b)))
	}
	arm, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	return string(arm), nil
}

// cleanBicepError distills raw bicep CLI output (upgrade warnings, temp-file
// paths, doc-link suffixes) down to the meaningful error, so callers can surface
// a readable message. The synthetic wrapper's BCP035 on "params" specifically
// means the module has required inputs that weren't provided.
func cleanBicepError(raw string) string {
	var out []string
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "WARNING") {
			continue
		}
		if strings.Contains(ln, "BCP035") && strings.Contains(ln, `"params"`) {
			return "this module has required inputs that aren't set"
		}
		if i := strings.Index(ln, "Error "); i >= 0 { // drop the "<path>(l,c) : " prefix
			ln = ln[i:]
		}
		if j := strings.Index(ln, " [https://"); j >= 0 { // drop the aka.ms doc link
			ln = ln[:j]
		}
		ln = strings.TrimPrefix(ln, "ERROR: ")
		if ln != "" {
			out = append(out, ln)
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(raw)
	}
	return strings.Join(out, "; ")
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

// ParamSpec describes one module input parameter, for rendering an authoring form.
type ParamSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`              // string | int | bool | object | array | securestring | secureobject
	Required    bool   `json:"required"`          // no default and not nullable
	Default     any    `json:"default,omitempty"` // primitive/JSON default, when present
	Allowed     []any  `json:"allowed,omitempty"` // allowedValues (→ a dropdown)
	Description string `json:"description,omitempty"`
	Secure      bool   `json:"secure,omitempty"` // securestring/secureobject
}

// OutputSpec describes one module output (name + type), for the wiring board.
type OutputSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Inspect returns a module's input parameters + outputs so the console can render
// a typed authoring form (instead of raw JSON) and show wireable outputs before
// the deployment is even saved. It works for a module with required params — it
// restores the module to the local cache (no values needed) and reads its
// compiled interface — or an inline ARM template (parsed directly).
func Inspect(ctx context.Context, ref string) ([]ParamSpec, []OutputSpec, error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return nil, nil, nil
	}
	if strings.HasPrefix(s, "{") { // inline ARM — read its interface directly
		p, o := parseModuleInterface([]byte(s))
		return p, o, nil
	}
	if !isModuleRef(s) {
		return nil, nil, ErrBadRef
	}
	if !Available() {
		return nil, nil, ErrNoCompiler
	}
	dir, err := os.MkdirTemp("", "cortex-bicep-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)

	// Restore (not build) the module — this pulls it to the cache without needing
	// its required params satisfied, unlike a build.
	in := filepath.Join(dir, "main.bicep")
	if err := os.WriteFile(in, []byte(fmt.Sprintf("module infra '%s' = {\n  name: 'infra'\n}\n", s)), 0o600); err != nil {
		return nil, nil, err
	}
	cmd, err := restoreCmd(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("%s", cleanBicepError(string(b)))
	}
	arm, err := readCachedModule(s)
	if err != nil {
		return nil, nil, err
	}
	p, o := parseModuleInterface(arm)
	return p, o, nil
}

func restoreCmd(ctx context.Context, file string) (*exec.Cmd, error) {
	if p, err := exec.LookPath("bicep"); err == nil {
		return exec.CommandContext(ctx, p, "restore", file, "--force"), nil
	}
	if p, err := exec.LookPath("az"); err == nil {
		return exec.CommandContext(ctx, p, "bicep", "restore", "--file", file, "--force"), nil
	}
	return nil, ErrNoCompiler
}

// readCachedModule reads a restored module's compiled ARM from the bicep module
// cache (~/.bicep/br/<registry>/<repo with / → $>/<tag>$/main.json), falling back
// to a directed walk if the layout differs.
func readCachedModule(ref string) ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	registry, repo, tag, err := splitModuleRef(ref)
	if err != nil {
		return nil, err
	}
	direct := filepath.Join(home, ".bicep", "br", registry, strings.ReplaceAll(repo, "/", "$"), tag+"$", "main.json")
	if b, err := os.ReadFile(direct); err == nil {
		return b, nil
	}
	// Fallback: walk the cache for a main.json under a "<tag>$" dir matching the
	// repo's last segment (handles registry alias / path-encoding differences).
	seg := repo
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		seg = repo[i+1:]
	}
	root := filepath.Join(home, ".bicep", "br")
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d.IsDir() || d.Name() != "main.json" {
			return nil
		}
		parent := filepath.Base(filepath.Dir(path))
		if parent == tag+"$" && strings.Contains(path, seg) {
			found = path
		}
		return nil
	})
	if found == "" {
		return nil, fmt.Errorf("restored module not found in cache for %q", ref)
	}
	return os.ReadFile(found)
}

// splitModuleRef parses an OCI module ref into (registry, repo, tag). The
// br/public alias maps to mcr.microsoft.com/bicep.
func splitModuleRef(ref string) (registry, repo, tag string, err error) {
	s := ref
	switch {
	case strings.HasPrefix(s, "br/public:"):
		s = "mcr.microsoft.com/bicep/" + strings.TrimPrefix(s, "br/public:")
	case strings.HasPrefix(s, "br:"):
		s = strings.TrimPrefix(s, "br:")
	case strings.HasPrefix(s, "oci://"):
		s = strings.TrimPrefix(s, "oci://")
	default:
		return "", "", "", ErrBadRef
	}
	slash := strings.IndexByte(s, '/')
	colon := strings.LastIndexByte(s, ':')
	if slash < 0 || colon < 0 || colon < slash {
		return "", "", "", ErrBadRef
	}
	return s[:slash], s[slash+1 : colon], s[colon+1:], nil
}

// parseModuleInterface reads parameter + output schemas from a compiled ARM
// template. Params are sorted required-first then by name; AVM's "Required. " /
// "Optional. " description prefixes are trimmed.
func parseModuleInterface(arm []byte) ([]ParamSpec, []OutputSpec) {
	var t struct {
		Parameters map[string]struct {
			Type          string          `json:"type"`
			DefaultValue  json.RawMessage `json:"defaultValue"`
			AllowedValues []any           `json:"allowedValues"`
			Nullable      bool            `json:"nullable"`
			Metadata      struct {
				Description string `json:"description"`
			} `json:"metadata"`
		} `json:"parameters"`
		Outputs map[string]struct {
			Type string `json:"type"`
		} `json:"outputs"`
	}
	if json.Unmarshal(arm, &t) != nil {
		return nil, nil
	}
	params := make([]ParamSpec, 0, len(t.Parameters))
	for name, p := range t.Parameters {
		hasDefault := len(p.DefaultValue) > 0
		var def any
		if hasDefault {
			_ = json.Unmarshal(p.DefaultValue, &def)
		}
		lt := strings.ToLower(p.Type)
		params = append(params, ParamSpec{
			Name:        name,
			Type:        lt,
			Required:    !hasDefault && !p.Nullable,
			Default:     def,
			Allowed:     p.AllowedValues,
			Description: trimDescPrefix(p.Metadata.Description),
			Secure:      strings.HasPrefix(lt, "secure"),
		})
	}
	sort.Slice(params, func(i, j int) bool {
		if params[i].Required != params[j].Required {
			return params[i].Required // required first
		}
		return params[i].Name < params[j].Name
	})
	outputs := make([]OutputSpec, 0, len(t.Outputs))
	for name, o := range t.Outputs {
		outputs = append(outputs, OutputSpec{Name: name, Type: strings.ToLower(o.Type)})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	return params, outputs
}

func trimDescPrefix(d string) string {
	for _, p := range []string{"Required. ", "Optional. ", "Conditional. "} {
		if strings.HasPrefix(d, p) {
			return strings.TrimPrefix(d, p)
		}
	}
	return d
}
