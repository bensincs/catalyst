package bicep

import (
	"strings"
	"testing"
)

// The point of secret-bound Bicep parameters: a value must not end up as a
// literal in the compiled template. A literal there is preserved in the Azure
// deployment history permanently and is readable by anyone with reader access on
// the resource group — which is where every infrastructure password lived before
// this existed.

func TestSecretParamsAreDeclaredNotBaked(t *testing.T) {
	params := map[string]any{
		"administratorLogin": "cortex",
		"administratorLoginPassword": map[string]any{
			SecretRefKey: map[string]any{"setId": "db-creds", "key": "password"},
		},
	}
	plain, secrets := splitSecretParams(params)

	if _, baked := plain["administratorLoginPassword"]; baked {
		t.Fatal("the secret-bound param was left among the literals — it would be baked into the template")
	}
	if plain["administratorLogin"] != "cortex" {
		t.Fatal("an ordinary param must still be baked; only secrets are declared")
	}
	if len(secrets) != 1 || secrets[0].Param != "administratorLoginPassword" ||
		secrets[0].SetID != "db-creds" || secrets[0].Key != "password" {
		t.Fatalf("binding not extracted: %+v", secrets)
	}

	src := wrapper("br:x/y:1", nil, plain, secrets)
	if !strings.Contains(src, "@secure()\nparam administratorLoginPassword string") {
		t.Fatalf("no @secure() declaration emitted:\n%s", src)
	}
	// The parameter must be passed through by reference, not as a quoted string.
	if !strings.Contains(src, "administratorLoginPassword: administratorLoginPassword") {
		t.Fatalf("secret param not passed by reference:\n%s", src)
	}
	if strings.Contains(src, "'administratorLoginPassword'") {
		t.Fatalf("secret param rendered as a string literal:\n%s", src)
	}
}

func TestKeyVaultParametersCarryNoValue(t *testing.T) {
	name := func(setID, key string) string { return "set-" + setID + "--" + key }
	got := KeyVaultParameters("/subscriptions/s/.../vaults/kv",
		[]SecretBinding{{Param: "adminPassword", SetID: "db-creds", Key: "password"}}, name)

	p, ok := got["adminPassword"].(map[string]any)
	if !ok {
		t.Fatalf("no parameter produced: %#v", got)
	}
	// An ARM parameter is either {"value": ...} or {"reference": ...}. It must be
	// the latter — a "value" here would mean the control plane had read the
	// secret, which it cannot and must not do.
	if _, hasValue := p["value"]; hasValue {
		t.Fatal("parameter carries a value; the control plane must never hold the secret")
	}
	ref, ok := p["reference"].(map[string]any)
	if !ok {
		t.Fatalf("parameter is not a Key Vault reference: %#v", p)
	}
	if ref["secretName"] != "set-db-creds--password" {
		t.Fatalf("wrong secret name: %v", ref["secretName"])
	}
}

func TestNoVaultMeansNoParameters(t *testing.T) {
	// Without a vault there is nothing to reference; producing a half-built
	// parameter object would fail the deployment with an opaque ARM error.
	if got := KeyVaultParameters("", []SecretBinding{{Param: "p", SetID: "s", Key: "k"}}, nil); got != nil {
		t.Fatalf("expected no parameters without a vault, got %#v", got)
	}
}

func TestMalformedSecretRefIsNotSilentlyDropped(t *testing.T) {
	// A marker missing its key is an authoring mistake. Treating it as a literal
	// surfaces a Bicep type error the author can see, whereas dropping it would
	// produce a deployment that is quietly missing a required parameter.
	plain, secrets := splitSecretParams(map[string]any{
		"p": map[string]any{SecretRefKey: map[string]any{"setId": "s"}},
	})
	if len(secrets) != 0 {
		t.Fatalf("incomplete binding was accepted: %+v", secrets)
	}
	if _, kept := plain["p"]; !kept {
		t.Fatal("incomplete binding vanished instead of surfacing as an error")
	}
}

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
