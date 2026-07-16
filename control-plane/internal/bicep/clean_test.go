package bicep

import "testing"

func TestCleanBicepError(t *testing.T) {
	bcp035 := "WARNING: A new Bicep release is available: v0.45.15. Upgrade now.\n" +
		`ERROR: /var/folders/x/T/cortex-bicep-1/main.bicep(1,8) : Error BCP035: The specified "module" declaration is missing the following required properties: "params". [https://aka.ms/bicep/core-diagnostics#BCP035]`
	if got := cleanBicepError(bcp035); got != "this module has required inputs that aren't set" {
		t.Fatalf("BCP035: got %q", got)
	}

	other := "WARNING: upgrade\n" +
		`ERROR: /tmp/main.bicep(3,5) : Error BCP033: Expected a value of type "int". [https://aka.ms/x]`
	if got := cleanBicepError(other); got != `Error BCP033: Expected a value of type "int".` {
		t.Fatalf("BCP033: got %q", got)
	}

	if got := cleanBicepError("something unexpected"); got != "something unexpected" {
		t.Fatalf("fallback: got %q", got)
	}
}
