package httpapi

import "testing"

// An application's chart reference is stored and later handed to Argo, which
// runs its own helm with it. Rejecting a bad reference only at inspection time
// leaves the value to fail somewhere far less obvious, so the write path applies
// the same rules.
func TestAppInputRejectsInjectableReference(t *testing.T) {
	bad := []appInput{
		{Name: "x", RepoURL: "https://example.com", Chart: "--destination=/app"},
		{Name: "x", RepoURL: "--repository-config=/tmp/x", Chart: "nginx"},
		{Name: "x", RepoURL: "file:///etc", Chart: "nginx"},
		{Name: "x", RepoURL: "https://example.com", Chart: "nginx", TargetRevision: "-d"},
	}
	for _, in := range bad {
		if msg := in.validate(); msg == "" {
			t.Errorf("accepted %+v", in)
		}
	}
}

func TestAppInputAcceptsRealReferences(t *testing.T) {
	ok := []appInput{
		{Name: "todo", RepoURL: "oci://reg.azurecr.io/charts", Chart: "todo-app", TargetRevision: "0.1.0"},
		{Name: "nginx", RepoURL: "https://kubernetes.github.io/ingress-nginx", Chart: "ingress-nginx", TargetRevision: "4.15.1"},
	}
	for _, in := range ok {
		if msg := in.validate(); msg != "" {
			t.Errorf("rejected %+v: %s", in, msg)
		}
	}
	// The existing required-field rules still apply.
	if msg := (appInput{Name: "", RepoURL: "https://x", Chart: "c"}).validate(); msg == "" {
		t.Error("a missing name must still be rejected")
	}
}
