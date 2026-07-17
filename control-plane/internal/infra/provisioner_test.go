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
