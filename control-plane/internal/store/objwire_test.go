package store

import (
	"strings"
	"testing"

	"github.com/inception42/cortex/shared"
)

// The insight contract is a single wire: the module's `appInfra` OBJECT output
// onto the chart's `.Values.appInfra`. Everything else wires scalars, so this is
// the first time an output is a whole map — and if applyWiring flattened or
// stringified it, the chart would receive nonsense.
func TestWiringCarriesAnObjectOutput(t *testing.T) {
	sources := map[string]map[string]any{
		"infrastructure:insight-infra": {
			"appInfra": map[string]any{
				"keyvaultUrl":              "https://kv.vault.azure.net/",
				"workloadIdentityClientId": "abc-123",
				"externalSecrets":          map[string]any{"enabled": true, "serviceAccountName": "backend-sa"},
			},
		},
	}
	out := applyWiring("global:\n  namespace: insight\n", []shared.WireLink{
		{SourceKind: "infrastructure", SourceID: "insight-infra", Output: "appInfra", HelmPath: "appInfra"},
	}, sources)

	for _, want := range []string{
		"keyvaultUrl: https://kv.vault.azure.net/",
		"workloadIdentityClientId: abc-123",
		"serviceAccountName: backend-sa",
		"enabled: true",
		"namespace: insight",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in wired values, got:\n%s", want, out)
		}
	}
}
