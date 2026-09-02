package bicep

import "testing"

// A module's secure outputs must not be published. Re-exporting one at the top
// level strips the secureness — the value lands in cleartext in the deployment's
// properties.outputs, is stored in infra_outputs, and is then merged into a
// dependent app's Helm values by wiring. So a module that carefully declared its
// connection string @secure() had that protection removed by the re-export.

func TestSecureOutputsAreNotReExported(t *testing.T) {
	// A module output declared securestring loses its protection when re-exported
	// at the top level: it lands in cleartext in the deployment's outputs, is
	// stored in infra_outputs, and is merged into a dependent app's Helm values.
	arm := `{"resources":[{"type":"Microsoft.Resources/deployments","properties":{"template":{"outputs":{
	  "host":{"type":"string"},
	  "connectionString":{"type":"securestring"},
	  "config":{"type":"secureObject"}}}}}]}`

	outs := moduleOutputTypes(arm)
	if _, leaked := outs["connectionString"]; leaked {
		t.Error("a securestring output was re-exported, which publishes it in cleartext")
	}
	if _, leaked := outs["config"]; leaked {
		t.Error("a secureObject output was re-exported, which publishes it in cleartext")
	}
	if outs["host"] != "string" {
		t.Errorf("ordinary outputs must still be re-exported, got %q", outs["host"])
	}
}

func TestArmTypeToBicepKeepsObjectShape(t *testing.T) {
	// secureObject previously collapsed to "string", so a re-exported object
	// output was declared with the wrong type entirely.
	if got := armTypeToBicep("secureObject"); got != "object" {
		t.Errorf("secureObject → %q, want object", got)
	}
	if got := armTypeToBicep("securestring"); got != "string" {
		t.Errorf("securestring → %q, want string", got)
	}
	if got := armTypeToBicep("object"); got != "object" {
		t.Errorf("object → %q, want object", got)
	}
	if got := armTypeToBicep("array"); got != "array" {
		t.Errorf("array → %q, want array", got)
	}
}

// Inspect drives the authoring form's list of wireable outputs, and Resolve
// decides what the deployment actually exports. They must agree: an output the
// console offers but the deployment drops is a binding that silently resolves to
// nothing at runtime.
func TestInspectAndResolveAgreeOnSecureOutputs(t *testing.T) {
	module := []byte(`{"parameters":{},"outputs":{
	  "host":{"type":"string"},
	  "connectionString":{"type":"securestring"},
	  "helmValues":{"type":"secureObject"}}}`)

	_, outs := parseModuleInterface(module)
	for _, o := range outs {
		if o.Name == "connectionString" || o.Name == "helmValues" {
			t.Errorf("Inspect offers secure output %q for wiring, but the deployment will not export it", o.Name)
		}
	}
	if len(outs) != 1 || outs[0].Name != "host" {
		t.Fatalf("ordinary outputs must still be offered, got %+v", outs)
	}
}
