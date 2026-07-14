package cluster

import (
	"testing"

	"github.com/inception42/cortex/shared"

	"sigs.k8s.io/yaml"
)

func TestApplyWiring(t *testing.T) {
	values := "replicaCount: 2\n"
	wiring := []shared.WireLink{
		{BicepOutput: "dbHost", HelmPath: "database.host"},
		{BicepOutput: "dbPass", HelmPath: "database.password"},
		{BicepOutput: "missing", HelmPath: "x.y"}, // output absent → not wired
	}
	outputs := map[string]string{"dbHost": "sql.example.net", "dbPass": "s3cret"}

	got := applyWiring(values, wiring, outputs)

	var m map[string]any
	if err := yaml.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("result not valid yaml: %v\n%s", err, got)
	}
	db, _ := m["database"].(map[string]any)
	if db == nil || db["host"] != "sql.example.net" || db["password"] != "s3cret" {
		t.Fatalf("wiring not applied: %s", got)
	}
	if m["replicaCount"] == nil {
		t.Fatalf("base values lost: %s", got)
	}
	if _, ok := m["x"]; ok {
		t.Fatalf("absent output should not wire a path: %s", got)
	}

	// No wiring or no outputs → values untouched.
	if applyWiring(values, nil, outputs) != values {
		t.Fatalf("nil wiring should be a no-op")
	}
	if applyWiring(values, wiring, nil) != values {
		t.Fatalf("nil outputs should be a no-op")
	}
}
