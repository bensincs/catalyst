package chart

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildInterface(t *testing.T) {
	values := []byte("replicaCount: 2\nimage:\n  repository: nginx\n  tag: \"1.27\"\nservice:\n  type: ClusterIP\n  port: 80\n")
	schema := []byte(`{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"replicaCount":{"type":"integer"}}}`)
	chartYAML := []byte("apiVersion: v2\nname: demo\nversion: 1.4.2\ndescription: A demo chart\n")

	iface := buildInterface(values, schema, chartYAML)
	if iface.Name != "demo" || iface.Version != "1.4.2" || iface.Description != "A demo chart" {
		t.Fatalf("metadata: %+v", iface)
	}
	// Defaults are values.yaml converted to JSON (the value tree).
	var d map[string]any
	if err := json.Unmarshal(iface.Defaults, &d); err != nil {
		t.Fatalf("defaults not json: %v", err)
	}
	if d["replicaCount"] != float64(2) {
		t.Fatalf("replicaCount: %v", d["replicaCount"])
	}
	img, _ := d["image"].(map[string]any)
	if img["repository"] != "nginx" {
		t.Fatalf("nested image.repository: %v", d["image"])
	}
	if !json.Valid(iface.Schema) || !strings.Contains(string(iface.Schema), "replicaCount") {
		t.Fatalf("schema not preserved: %s", iface.Schema)
	}
}

func TestBuildInterfaceEmptyValues(t *testing.T) {
	// A chart with no/empty values.yaml and no schema still yields a usable {} tree.
	iface := buildInterface(nil, nil, []byte("name: bare\nversion: 0.1.0\n"))
	if string(iface.Defaults) != "{}" {
		t.Fatalf("empty defaults should be {}: %s", iface.Defaults)
	}
	if iface.Schema != nil {
		t.Fatalf("no schema expected")
	}
	if iface.Name != "bare" {
		t.Fatalf("name: %s", iface.Name)
	}
}
