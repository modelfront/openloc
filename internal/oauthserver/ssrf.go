package oauthserver

import (
	"context"
	"net"

	"openloc/internal/netguard"
)

// newCIMDDialContext is the DialContext the CIMD client uses. A CIMD
// client_id is an attacker-supplied https URL fetched server-side BEFORE the
// browser session is established, so the dialer refuses non-public addresses.
func newCIMDDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return netguard.DialContext(cimdFetchTimeout)
}
