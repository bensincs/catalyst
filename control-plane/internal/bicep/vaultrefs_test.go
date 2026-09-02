package bicep

import (
	"reflect"
	"testing"
)

// The shape a module produces when it resolves its own credential: the reference
// lives in a nested deployment's parameters and names a parameter, whose literal
// the enclosing deployment supplies.
const wrappedKVTemplate = `{
  "resources": [{
    "type": "Microsoft.Resources/deployments",
    "properties": {
      "parameters": {
        "vaultName": { "value": "cortex-kv-xyz" },
        "passwordSecretName": { "value": "set-todo-database--password" }
      },
      "template": {
        "resources": [{
          "type": "Microsoft.Resources/deployments",
          "properties": {
            "parameters": {
              "administratorLoginPassword": {
                "reference": {
                  "keyVault": { "id": "[resourceId('Microsoft.KeyVault/vaults', parameters('vaultName'))]" },
                  "secretName": "[parameters('passwordSecretName')]"
                }
              }
            },
            "template": {}
          }
        }]
      }
    }
  }]
}`

func TestVaultSecretRefsFollowsTheParameterChain(t *testing.T) {
	got := VaultSecretRefs(wrappedKVTemplate)
	if want := []string{"set-todo-database--password"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestVaultSecretRefsReadsALiteralName(t *testing.T) {
	arm := `{"resources":[{"properties":{"parameters":{
	  "p":{"reference":{"keyVault":{"id":"/x"},"secretName":"plain-name"}}},"template":{}}}]}`
	if got := VaultSecretRefs(arm); !reflect.DeepEqual(got, []string{"plain-name"}) {
		t.Fatalf("got %v", got)
	}
}

func TestVaultSecretRefsSkipsWhatItCannotResolve(t *testing.T) {
	// A name built at runtime cannot be known here. Reporting a guess would be
	// worse than reporting none: the deployment would be held waiting for a
	// secret nobody is being asked for.
	arm := `{"resources":[{"properties":{"parameters":{
	  "p":{"reference":{"keyVault":{"id":"/x"},"secretName":"[concat('a', variables('b'))]"}}},"template":{}}}]}`
	if got := VaultSecretRefs(arm); len(got) != 0 {
		t.Fatalf("guessed a name from a runtime expression: %v", got)
	}
}

func TestVaultSecretRefsIgnoresOrdinaryParameters(t *testing.T) {
	// Only Key Vault references matter. A normal parameter, even one whose name
	// mentions a secret, is not one.
	arm := `{"resources":[{"properties":{"parameters":{
	  "passwordSecretName":{"value":"set-x--password"},
	  "adminLogin":{"value":"todoadmin"}},"template":{}}}]}`
	if got := VaultSecretRefs(arm); len(got) != 0 {
		t.Fatalf("treated a plain parameter as a vault reference: %v", got)
	}
}

func TestVaultSecretRefsHandlesNoTemplate(t *testing.T) {
	if got := VaultSecretRefs("not json"); got != nil {
		t.Fatalf("got %v", got)
	}
	if got := VaultSecretRefs(`{"resources":[]}`); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestVaultSecretRefsDeduplicates(t *testing.T) {
	arm := `{"resources":[
	  {"properties":{"parameters":{"a":{"reference":{"keyVault":{"id":"/x"},"secretName":"same"}}},"template":{}}},
	  {"properties":{"parameters":{"b":{"reference":{"keyVault":{"id":"/x"},"secretName":"same"}}},"template":{}}}
	]}`
	if got := VaultSecretRefs(arm); len(got) != 1 {
		t.Fatalf("expected one distinct name, got %v", got)
	}
}
