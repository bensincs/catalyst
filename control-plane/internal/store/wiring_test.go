package store

import (
	"strings"
	"testing"

	"github.com/inception42/cortex/shared"
)

func TestApplyWiring(t *testing.T) {
	values := "replicaCount: 2\n"
	wiring := []shared.WireLink{
		{Infrastructure: "db", BicepOutput: "host", HelmPath: "database.host"},
		{Infrastructure: "db", BicepOutput: "port", HelmPath: "database.port"},
	}
	infraOutputs := map[string]map[string]any{"db": {"host": "db.example.com", "port": float64(5432)}}

	got := applyWiring(values, wiring, infraOutputs)
	for _, want := range []string{"database:", "host: db.example.com", "port: 5432", "replicaCount: 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wired values missing %q:\n%s", want, got)
		}
	}

	// No wiring or no outputs → values unchanged.
	if applyWiring(values, nil, infraOutputs) != values {
		t.Fatalf("no wiring should be a no-op")
	}
	if applyWiring(values, wiring, nil) != values {
		t.Fatalf("no outputs should be a no-op")
	}
	// An output from an infrastructure the app doesn't depend on is ignored.
	if applyWiring(values, []shared.WireLink{{Infrastructure: "other", BicepOutput: "host", HelmPath: "x"}}, infraOutputs) != values {
		t.Fatalf("wiring to an unknown infrastructure should be a no-op")
	}
	// Unparseable YAML is left untouched (defensive).
	bad := ": : not yaml"
	if applyWiring(bad, wiring, infraOutputs) != bad {
		t.Fatalf("unparseable values should be returned unchanged")
	}
}

func TestSetNestedCreatesPath(t *testing.T) {
	m := map[string]any{}
	setNested(m, []string{"a", "b", "c"}, 42)
	a, _ := m["a"].(map[string]any)
	b, _ := a["b"].(map[string]any)
	if b["c"] != 42 {
		t.Fatalf("nested set failed: %#v", m)
	}
}
