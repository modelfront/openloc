package netguard

import (
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// dialTimeout bounds the TCP connect; the client's own timeout bounds the
// whole request.
const dialTimeout = 10 * time.Second

// DevMode reports whether ENV is "development" (config's default when unset).
// Read from the environment on every call, like mtdefaults does, because the
// callers hold no config.
func DevMode() bool {
	v := strings.TrimSpace(os.Getenv("ENV"))
	return v == "" || v == "development"
}

// Client returns an http.Client for URLs a tenant controls (vendor base URLs).
// Every connection, each redirect hop included, is dialled through the
// public-address check, so a redirect cannot bounce a call onto an internal
// service. In development the check is off: the dev stack's local engines,
// gateways and test servers live on loopback and docker addresses.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transport(func(ip net.IP) bool {
		return DevMode() || IsPublicIP(ip)
	})}
}

// transport dials only addresses allow accepts. No proxy: a proxy would make
// the dialled address the proxy's, not the target's.
func transport(allow func(net.IP) bool) *http.Transport {
	return &http.Transport{
		DialContext:           guardedDialer(dialTimeout, allow).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
