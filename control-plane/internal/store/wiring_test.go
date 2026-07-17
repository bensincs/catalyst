package store

import (
	"strings"
	"testing"

	"github.com/inception42/cortex/shared"
)

func TestApplyWiring(t *testing.T) {
	values := "replicaCount: 2\n"
	wiring := []shared.WireLink{
		{SourceKind: "infrastructure", SourceID: "db", Output: "host", HelmPath: "database.host"},
		{SourceKind: "infrastructure", SourceID: "db", Output: "port", HelmPath: "database.port"},
		{SourceKind: "agent", SourceID: "triage", Output: "agentId", HelmPath: "agent.id"},
	}
	sources := map[string]map[string]any{
		"infrastructure:db": {"host": "db.example.com", "port": float64(5432)},
		"agent:triage":      {"agentId": "triage", "name": "Triage"},
	}

	got := applyWiring(values, wiring, sources)
	for _, want := range []string{"database:", "host: db.example.com", "port: 5432", "replicaCount: 2", "agent:", "id: triage"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wired values missing %q:\n%s", want, got)
		}
	}

	// No wiring or no sources → values unchanged.
	if applyWiring(values, nil, sources) != values {
		t.Fatalf("no wiring should be a no-op")
	}
	if applyWiring(values, wiring, nil) != values {
		t.Fatalf("no sources should be a no-op")
	}
	// A wire to a source the app doesn't depend on is ignored.
	if applyWiring(values, []shared.WireLink{{SourceKind: "infrastructure", SourceID: "other", Output: "host", HelmPath: "x"}}, sources) != values {
		t.Fatalf("wiring to an unknown source should be a no-op")
	}
	// Unparseable YAML is left untouched (defensive).
	bad := ": : not yaml"
	if applyWiring(bad, wiring, sources) != bad {
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
