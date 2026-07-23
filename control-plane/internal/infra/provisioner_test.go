package infra

import (
	"strings"
	"testing"
)

func TestSubstituteTokens(t *testing.T) {
	arm := `{"name":"cortexkv{{tenantHash}}","tenant":"{{tenant}}","loc":"{{region}}"}`
	out := substituteTokens(arm, "t-cff8707ddd78", "uaenorth")
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
