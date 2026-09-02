package chart

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Input validation for values that become argv elements.
//
// repoURL, chart and version arrive from an HTTP request and are appended to a
// `helm` command line. There is no shell, so this is not shell injection — but
// argv is not inert either: helm reads any element beginning with "-" as a
// flag. A crafted chart name of "--repository-config=/path" is consumed as a
// flag rather than a chart, and from there an attacker chooses --ca-file,
// --destination, --untardir and friends on a process that holds every tenant's
// secrets. Confirmed against helm 3.16: the positional argument is eaten.
//
// The endpoint is reachable by any authenticated caller, including a customer's
// own tenant admin, so this crosses a trust boundary.

// ErrBadInput is returned when a reference cannot be used safely.
var ErrBadInput = fmt.Errorf("invalid chart reference")

// A chart name is a DNS-ish label path: letters, digits, dot, dash, underscore,
// and slashes for a repo-qualified name. Deliberately no leading dash.
var chartNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*$`)

// A version is semver-ish, plus the range characters Helm accepts. Again, no
// leading dash — "-1.0.0" would be read as a flag.
var versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_~^*<>=-]*$`)

// ValidateRef checks the three caller-supplied parts of a chart reference.
// Exported so the authoring API can reject a bad reference at write time: a
// stored one is handed to Argo later, which runs its own helm with it, so
// refusing it only at inspection time leaves the value to fail somewhere less
// obvious.
func ValidateRef(repoURL, chart, version string) error { return validateRef(repoURL, chart, version) }

// validateRef checks the three caller-supplied parts of a chart reference.
func validateRef(repoURL, chart, version string) error {
	repoURL, chart, version = strings.TrimSpace(repoURL), strings.TrimSpace(chart), strings.TrimSpace(version)

	if repoURL != "" {
		u, err := url.Parse(repoURL)
		if err != nil {
			return fmt.Errorf("%w: repository is not a URL", ErrBadInput)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "oci":
		default:
			// Also rejects a bare "-flag", which parses with an empty scheme.
			return fmt.Errorf("%w: repository must be http(s):// or oci://", ErrBadInput)
		}
		if u.Host == "" {
			return fmt.Errorf("%w: repository has no host", ErrBadInput)
		}
	}
	if chart != "" && !chartNameRe.MatchString(chart) {
		return fmt.Errorf("%w: chart name", ErrBadInput)
	}
	if version != "" && !versionRe.MatchString(version) {
		return fmt.Errorf("%w: chart version", ErrBadInput)
	}
	return nil
}

// validateRelease checks the release name used for `helm template`. It is
// server-derived today, but it reaches argv the same way, so it is checked
// rather than trusted.
func validateRelease(release string) error {
	if release == "" {
		return nil
	}
	if !chartNameRe.MatchString(release) || strings.Contains(release, "/") {
		return fmt.Errorf("%w: release name", ErrBadInput)
	}
	return nil
}
