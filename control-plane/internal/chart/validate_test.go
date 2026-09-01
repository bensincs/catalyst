package chart

import (
	"errors"
	"strings"
	"testing"
)

// These values become argv elements for `helm`. There is no shell, so the risk
// is not shell injection but argument injection: helm reads anything starting
// with "-" as a flag. Confirmed against helm 3.16 — a chart name of
// "--repository-config=..." is consumed as a flag and the positional argument
// disappears. The endpoint is reachable by any authenticated caller, including
// a customer's tenant admin, and the process holds every tenant's secrets.
func TestValidateRefRejectsArgumentInjection(t *testing.T) {
	// Every one of these is a real helm flag that changes where it reads or
	// writes files.
	injections := []struct{ repo, chart, version string }{
		{"", "--repository-config=/tmp/evil.yaml", ""},
		{"", "--registry-config=/proc/self/environ", ""},
		{"", "--destination=/app", ""},
		{"", "--untardir=/", ""},
		{"", "--ca-file=/etc/passwd", ""},
		{"", "-d", ""},
		{"", "nginx", "--destination=/app"},
		{"", "nginx", "-d"},
		{"--repository-config=/tmp/x", "nginx", ""},
		{"-oci://ghcr.io/x", "nginx", ""},
	}
	for _, c := range injections {
		if err := validateRef(c.repo, c.chart, c.version); err == nil {
			t.Errorf("accepted an injectable reference: repo=%q chart=%q version=%q", c.repo, c.chart, c.version)
		} else if !errors.Is(err, ErrBadInput) {
			t.Errorf("wrong error type for %q: %v", c.chart, err)
		}
	}
}

func TestValidateRefRejectsUnusableSchemes(t *testing.T) {
	// file:// would read the control plane's own disk; a bare word is not a repo.
	for _, repo := range []string{
		"file:///etc",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"not-a-url",
		"https://",
	} {
		if err := validateRef(repo, "nginx", ""); err == nil {
			t.Errorf("accepted repository %q", repo)
		}
	}
}

func TestValidateRefAcceptsRealReferences(t *testing.T) {
	// The validation must not break the references the product actually uses.
	ok := []struct{ repo, chart, version string }{
		{"https://kubernetes.github.io/ingress-nginx", "ingress-nginx", "4.15.1"},
		{"oci://cortexcpacr6hy6uurw.azurecr.io/charts", "todo-app", "0.1.0"},
		{"oci://ghcr.io/bensincs/charts", "todo-app", ""},
		{"https://charts.bitnami.com/bitnami", "postgresql", "15.5.38"},
		{"", "", ""},
		{"https://example.com/x", "sub/chart_name.v2", "1.2.3-rc.1+build"},
	}
	for _, c := range ok {
		if err := validateRef(c.repo, c.chart, c.version); err != nil {
			t.Errorf("rejected a legitimate reference %q/%q@%q: %v", c.repo, c.chart, c.version, err)
		}
	}
}

func TestValidateRelease(t *testing.T) {
	if err := validateRelease("--set=x"); err == nil {
		t.Error("a flag-shaped release name must be rejected")
	}
	if err := validateRelease("t-8f646aa4a8c3c71e-todo"); err != nil {
		t.Errorf("rejected a real release name: %v", err)
	}
	// A slash would let the release escape into a path position.
	if err := validateRelease("a/b"); err == nil {
		t.Error("a release name containing a slash must be rejected")
	}
	if err := validateRelease(""); err != nil {
		t.Error("an empty release is legitimate — rendering is then skipped")
	}
}

func TestValidationIsReportedClearly(t *testing.T) {
	err := validateRef("", "--destination=/app", "")
	if err == nil || !strings.Contains(err.Error(), "chart name") {
		t.Errorf("the message should name the offending field, got %v", err)
	}
}
