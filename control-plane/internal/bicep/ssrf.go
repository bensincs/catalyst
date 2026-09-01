package bicep

import (
	"fmt"
	"net"
	"strings"
)

// Where a module may be fetched from.
//
// A module reference is caller-supplied and its registry host becomes the target
// of an outbound request from the control plane — which runs in Azure as a
// managed identity, inside a network that can reach things the caller cannot.
// Without a check, "br:169.254.169.254/x:1" points it at the Instance Metadata
// Service, and "br:10.0.0.1/x:1" at anything on the vnet. The response and its
// errors come back to the caller, so this reads as well as probes.
//
// Registries that host Bicep modules are public by definition, so refusing
// anything that resolves to a private, loopback, link-local or unspecified
// address costs nothing legitimate.

// ErrBlockedHost is returned for a registry that is not publicly routable.
var ErrBlockedHost = fmt.Errorf("registry host is not publicly routable")

// blockedIP reports whether an address is one the control plane must not be
// aimed at on a caller's behalf.
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// checkPublicHost resolves a registry host and refuses it unless every address
// it resolves to is publicly routable.
//
// Every address, not just the first: a name that resolves to one public and one
// private address would otherwise pass here and be reconnected to the private
// one. This narrows the window rather than closing it — a name whose answer
// changes between this check and the request is still possible — but it removes
// the trivial case of naming an internal address outright.
func checkPublicHost(hostPort string) error {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrBlockedHost)
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedHost, host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: %s does not resolve", ErrBlockedHost, host)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBlockedHost, host, ip)
		}
	}
	return nil
}
