package bicep

import (
	"context"
	"strings"
	"testing"
)

func TestResolvePassthroughAndEmpty(t *testing.T) {
	// An inline ARM template passes through, with its output names.
	arm := `{"outputs":{"host":{"type":"string"},"port":{"type":"int"}}}`
	got, outs, err := Resolve(context.Background(), arm)
	if err != nil || got != arm {
		t.Fatalf("passthrough: got %q err %v", got, err)
	}
	if len(outs) != 2 || outs[0] != "host" || outs[1] != "port" {
		t.Fatalf("output names: %v", outs)
	}
	if g, o, e := Resolve(context.Background(), "  "); e != nil || g != "" || o != nil {
		t.Fatalf("empty: %q %v %v", g, o, e)
	}
}

func TestResolveBadRef(t *testing.T) {
	if _, _, err := Resolve(context.Background(), "not a ref"); err != ErrBadRef {
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

	w := wrapper("br:acr.azurecr.io/bicep/db:1.0.0", outs)
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
