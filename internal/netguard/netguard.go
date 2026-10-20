// Package netguard keeps server-side HTTP calls to URLs a user controls
// (OAuth client metadata, extension endpoints, AI and MT vendor base URLs) off
// internal addresses. The check runs at dial time on the resolved IP, which
// also defeats DNS rebinding: a name that resolves public once and private
// later is still refused, because every connection's concrete address is
// checked.
package netguard

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"
)

// Dialer refuses to connect to any non-public address (loopback,
// link-local, private RFC 1918 / ULA, CGNAT, unspecified, multicast).
func Dialer(timeout time.Duration) *net.Dialer {
	return guardedDialer(timeout, IsPublicIP)
}

// guardedDialer refuses any address allow rejects.
func guardedDialer(timeout time.Duration, allow func(net.IP) bool) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("netguard: unparseable dial address %q", address)
			}
			ip := net.ParseIP(host)
			if ip == nil || !allow(ip) {
				return fmt.Errorf("netguard: refusing to connect to non-public address %q", host)
			}
			return nil
		},
	}
}

// DialContext is Dialer's DialContext, for an http.Transport.
func DialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return Dialer(timeout).DialContext
}

// IsPublicIP reports whether ip is a globally-routable unicast address.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return false
	}
	// IsPrivate covers RFC 1918 + ULA fc00::/7; add the ranges Go doesn't:
	// CGNAT 100.64.0.0/10 and the IPv4 "this host on this network" 0.0.0.0/8.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 0 {
			return false
		}
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
	}
	return true
}

// IsLocalhost reports whether host is one of the literal names for this
// machine: "localhost", "127.0.0.1" or "::1". It is the same list net/smtp
// uses to allow a plain-text login, so callers that decide the same thing
// stay in step with it.
func IsLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
