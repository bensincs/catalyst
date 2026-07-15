package bicep

import (
	"context"
	"strings"
	"testing"
)

func TestResolvePassthroughAndEmpty(t *testing.T) {
	// An inline ARM template passes through, with its output names.
	arm := `{"outputs":{"host":{"type":"string"},"port":{"type":"int"}}}`
	got, outs, err := Resolve(context.Background(), arm, nil)
	if err != nil || got != arm {
		t.Fatalf("passthrough: got %q err %v", got, err)
	}
	if len(outs) != 2 || outs[0] != "host" || outs[1] != "port" {
		t.Fatalf("output names: %v", outs)
	}
	if g, o, e := Resolve(context.Background(), "  ", nil); e != nil || g != "" || o != nil {
		t.Fatalf("empty: %q %v %v", g, o, e)
	}
}

func TestResolveBadRef(t *testing.T) {
	if _, _, err := Resolve(context.Background(), "not a ref", nil); err != ErrBadRef {
		t.Fatalf("want ErrBadRef, got %v", err)
	}
}

func TestModuleOutputsAndWrapper(t *testing.T) {
	// A compiled wrapper ARM: the OCI module becomes a nested deployment whose
	// template carries the module's outputs (ARM types are capitalized).
	arm := `{"resources":[{"type":"Microsoft.Resources/deployments","properties":{"template":{"outputs":{"host":{"type":"String"},"port":{"type":"Int"}}}}}]}`
	outs := moduleOutputTypes(arm)
	if outs["host"] != "string" || outs["port"] != "int" {
		t.Fatalf("output types not mapped to bicep: %v", outs)
	}

	w := wrapper("br:acr.azurecr.io/bicep/db:1.0.0", outs, nil)
	for _, want := range []string{
		"module infra 'br:acr.azurecr.io/bicep/db:1.0.0'",
		"output host string = infra.outputs.host",
		"output port int = infra.outputs.port",
	} {
		if !strings.Contains(w, want) {
			t.Fatalf("wrapper missing %q:\n%s", want, w)
		}
	}
}

func TestWrapperParams(t *testing.T) {
	// Author params are baked into the module's params: block as Bicep literals.
	params := map[string]any{
		"name":     "cortex-db",
		"port":     float64(5432),
		"enabled":  true,
		"tags":     map[string]any{"env": "dev"},
		"zones":    []any{"1", "2"},
		"weird-id": "x",
	}
	w := wrapper("br:acr/db:1", nil, params)
	for _, want := range []string{
		"params: {",
		"name: 'cortex-db'",
		"port: 5432", // whole-number float → int (Bicep has no float)
		"enabled: true",
		"env: 'dev'",
		"'weird-id': 'x'", // non-identifier key is quoted
	} {
		if !strings.Contains(w, want) {
			t.Fatalf("wrapper params missing %q:\n%s", want, w)
		}
	}
	// No params → no params: block (works for zero-param modules).
	if strings.Contains(wrapper("br:acr/db:1", nil, nil), "params:") {
		t.Fatalf("empty params should omit the params block")
	}
}

func TestEscapeBicepString(t *testing.T) {
	if got := escapeBicepString(`it's ${x}`); got != `it\'s \${x}` {
		t.Fatalf("escape: %q", got)
	}
}

func TestIsModuleRef(t *testing.T) {
	for _, ok := range []string{"br:acr.azurecr.io/bicep/x:1.0", "oci://acr/x:1", "br/public:x"} {
		if !isModuleRef(ok) {
			t.Fatalf("expected module ref: %q", ok)
		}
	}
	for _, no := range []string{"acr.azurecr.io/x:1", "hello", "{}"} {
		if isModuleRef(no) {
			t.Fatalf("not a module ref: %q", no)
		}
	}
}

func TestParseModuleInterface(t *testing.T) {
	arm := []byte(`{
		"parameters": {
			"name":     {"type":"string","metadata":{"description":"Required. The name."}},
			"skuName":  {"type":"string","defaultValue":"Standard_GRS","allowedValues":["Standard_LRS","Standard_GRS"],"metadata":{"description":"Optional. The sku."}},
			"secret":   {"type":"securestring"},
			"tags":     {"type":"object","nullable":true}
		},
		"outputs": {"resourceId":{"type":"string"},"name":{"type":"string"}}
	}`)
	params, outputs := parseModuleInterface(arm)
	if len(params) != 4 {
		t.Fatalf("params: %d", len(params))
	}
	// Required-first ordering: name + secret are required (no default, not nullable).
	if params[0].Name != "name" && params[0].Name != "secret" {
		t.Fatalf("required not sorted first: %v", params[0])
	}
	by := map[string]ParamSpec{}
	for _, p := range params {
		by[p.Name] = p
	}
	if !by["name"].Required || by["name"].Description != "The name." {
		t.Fatalf("name spec: %+v", by["name"])
	}
	if by["skuName"].Required || by["skuName"].Default != "Standard_GRS" || len(by["skuName"].Allowed) != 2 {
		t.Fatalf("skuName spec: %+v", by["skuName"])
	}
	if !by["secret"].Secure || !by["secret"].Required {
		t.Fatalf("secret spec: %+v", by["secret"])
	}
	if by["tags"].Required { // nullable → optional
		t.Fatalf("nullable tags should be optional: %+v", by["tags"])
	}
	if len(outputs) != 2 || outputs[0].Name != "name" || outputs[1].Name != "resourceId" {
		t.Fatalf("outputs: %v", outputs)
	}
}

func TestSplitModuleRef(t *testing.T) {
	reg, repo, tag, err := splitModuleRef("br:mcr.microsoft.com/bicep/avm/res/storage/storage-account:0.32.1")
	if err != nil || reg != "mcr.microsoft.com" || repo != "bicep/avm/res/storage/storage-account" || tag != "0.32.1" {
		t.Fatalf("br: got %q %q %q %v", reg, repo, tag, err)
	}
	reg, repo, _, err = splitModuleRef("br/public:avm/res/key-vault/vault:0.13.3")
	if err != nil || reg != "mcr.microsoft.com" || repo != "bicep/avm/res/key-vault/vault" {
		t.Fatalf("br/public: got %q %q %v", reg, repo, err)
	}
	if _, _, _, err := splitModuleRef("not-a-ref"); err != ErrBadRef {
		t.Fatalf("want ErrBadRef, got %v", err)
	}
}
