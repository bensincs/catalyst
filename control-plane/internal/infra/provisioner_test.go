package infra

import (
	"context"
	"strings"
	"testing"

	"github.com/inception42/cortex/control-plane/internal/store"
)

func TestSubstituteTokens(t *testing.T) {
	arm := `{"name":"cortexkv{{tenantHash}}","tenant":"{{tenant}}","loc":"{{region}}","vault":"{{vaultName}}","vrg":"{{vaultResourceGroup}}"}`
	out := substituteTokens(arm, "t-cff8707ddd78", "uaenorth", "cortex-kv-abc123", "cortex-t-abc")
	if strings.Contains(out, "{{") {
		t.Fatalf("tokens not substituted: %s", out)
	}
	if !strings.Contains(out, `"tenant":"t-cff8707ddd78"`) || !strings.Contains(out, `"loc":"uaenorth"`) {
		t.Fatalf("tenant/region tokens wrong: %s", out)
	}
	// tenantHash is stable, 10 lowercase-hex chars, and distinct per tenant.
	h := tenantHash("t-cff8707ddd78")
	if tenantHash("t-cff8707ddd78") != h || len(h) != 10 {
		t.Fatalf("tenantHash unstable/wrong length: %q", h)
	}
	if tenantHash("t-other") == h {
		t.Fatalf("tenantHash collision")
	}
	// The tenant's vault name reaches the template so a module can resolve its
	// own credential from it — a name, never a value.
	if !strings.Contains(out, `"vault":"cortex-kv-abc123"`) {
		t.Fatalf("vaultName token not applied: %s", out)
	}
	// The vault lives in the tenant's footprint resource group, which is NOT the
	// one an application's infrastructure deploys into — a scope-less `existing`
	// reference would look in the wrong place.
	if !strings.Contains(out, `"vrg":"cortex-t-abc"`) {
		t.Fatalf("vaultResourceGroup token not applied: %s", out)
	}
	if !strings.Contains(out, "cortexkv"+h) {
		t.Fatalf("hash token not applied: %s", out)
	}
}

func TestParseResourceID(t *testing.T) {
	cases := []struct {
		id, sub, ns, rtype string
		ok                 bool
	}{
		{"/subscriptions/S/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv", "S", "Microsoft.KeyVault", "vaults", true},
		{"/subscriptions/S/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg", "S", "Microsoft.DBforPostgreSQL", "flexibleServers", true},
		{"/subscriptions/S/resourceGroups/rg/providers/Microsoft.Sql/servers/s/databases/d", "S", "Microsoft.Sql", "servers/databases", true},
		{"not a resource id", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		sub, ns, rt, ok := parseResourceID(c.id)
		if ok != c.ok || sub != c.sub || ns != c.ns || rt != c.rtype {
			t.Errorf("parseResourceID(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				c.id, sub, ns, rt, ok, c.sub, c.ns, c.rtype, c.ok)
		}
	}
}

func TestIsNestedResource(t *testing.T) {
	cases := map[string]bool{
		// Child resources — their type has 2+ segments; must be skippable so an
		// unresolvable child type never wedges a teardown.
		"/subscriptions/S/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg/advancedThreatProtectionSettings/current": true,
		"/subscriptions/S/resourceGroups/rg/providers/Microsoft.Sql/servers/s/databases/d":                                                   true,
		// Top-level resources — gate teardown completion.
		"/subscriptions/S/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg": false,
		"/subscriptions/S/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv":                 false,
		"not a resource id": false,
	}
	for id, want := range cases {
		if got := isNestedResource(id); got != want {
			t.Errorf("isNestedResource(%q) = %v, want %v", id, got, want)
		}
	}
}

// A template whose module resolves its own credential must not be submitted
// before the tenant has supplied it. ARM would fail the whole deployment with an
// error about a resource it could not read, which tells the operator nothing
// about the value somebody owes.
func TestMissingVaultSecretsHoldsWhenThereIsNoVaultYet(t *testing.T) {
	arm := `{"resources":[{"properties":{"parameters":{
	  "p":{"reference":{"keyVault":{"id":"/x"},"secretName":"set-todo-database--password"}}},"template":{}}}]}`

	p := &Provisioner{}
	// No vault recorded: it arrives with the tenant's footprint, so until then
	// there is nowhere for the value to be.
	got := p.missingVaultSecrets(context.Background(), store.InfraTarget{}, arm)
	if len(got) != 1 || got[0] != "set-todo-database--password" {
		t.Fatalf("expected the secret to be reported missing, got %v", got)
	}
}

func TestMissingVaultSecretsIgnoresTemplatesThatReadNone(t *testing.T) {
	// The overwhelming majority of modules take no credential. They must not be
	// held, and must not cost a vault round-trip.
	p := &Provisioner{}
	got := p.missingVaultSecrets(context.Background(),
		store.InfraTarget{VaultID: "/subscriptions/s/vaults/v"},
		`{"resources":[{"properties":{"parameters":{"name":{"value":"x"}},"template":{}}}]}`)
	if len(got) != 0 {
		t.Fatalf("held a deployment that reads no vault secret: %v", got)
	}
}

func TestResourceGroupOf(t *testing.T) {
	id := "/subscriptions/s/resourceGroups/cortex-t-abc/providers/Microsoft.KeyVault/vaults/kv"
	if got := resourceGroupOf(id); got != "cortex-t-abc" {
		t.Fatalf("resourceGroupOf = %q", got)
	}
	if got := resourceGroupOf("nonsense"); got != "" {
		t.Fatalf("expected empty for an unparseable id, got %q", got)
	}
}
