package bicep

import (
	"errors"
	"net"
	"testing"
)

// A module reference is caller-supplied and its host becomes an outbound request
// from the control plane, which runs as a managed identity inside a network the
// caller cannot otherwise reach.
func TestCheckPublicHostBlocksInternalTargets(t *testing.T) {
	blocked := map[string]string{
		"169.254.169.254":    "Azure Instance Metadata Service",
		"169.254.169.254:80": "IMDS with a port",
		"127.0.0.1":          "loopback",
		"127.0.0.1:5000":     "a local registry",
		"localhost":          "loopback by name",
		"10.0.0.1":           "RFC1918",
		"172.16.5.4":         "RFC1918",
		"192.168.1.1":        "RFC1918",
		"0.0.0.0":            "unspecified",
		"[::1]:443":          "IPv6 loopback",
		"[fe80::1]:443":      "IPv6 link-local",
		"":                   "empty",
	}
	for host, why := range blocked {
		err := checkPublicHost(host)
		if err == nil {
			t.Errorf("allowed %s (%s)", host, why)
			continue
		}
		if !errors.Is(err, ErrBlockedHost) {
			t.Errorf("%s: wrong error type: %v", host, err)
		}
	}
}

func TestCheckPublicHostAllowsRealRegistries(t *testing.T) {
	// The registries the product actually pulls modules from must still work.
	for _, host := range []string{
		"ghcr.io",
		"mcr.microsoft.com",
		"cortexcpacr6hy6uurw.azurecr.io",
	} {
		if err := checkPublicHost(host); err != nil {
			t.Errorf("blocked a real registry %s: %v", host, err)
		}
	}
}

func TestBlockedIPClassification(t *testing.T) {
	// Guards the classification itself, so a refactor cannot quietly narrow it.
	cases := map[string]bool{
		"8.8.8.8":         false,
		"1.1.1.1":         false,
		"169.254.169.254": true,
		"10.255.255.255":  true,
		"172.31.255.255":  true,
		"172.32.0.1":      false, // just outside RFC1918
		"192.167.0.1":     false, // just outside RFC1918
		"224.0.0.1":       true,  // multicast
	}
	for s, want := range cases {
		if got := blockedIP(parseIP(t, s)); got != want {
			t.Errorf("blockedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("unparseable test address %q", s)
	}
	return ip
}
