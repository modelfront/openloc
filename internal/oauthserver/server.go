package oauthserver

import (
	"net/http"

	db "openloc/internal/database/sqlcgen"
)

// Server carries the authorization-server dependencies. Mounted in api.go:
// /oauth/* + /.well-known/* live OUTSIDE the authenticated group (the whole
// point is that Claude/ChatGPT arrive without credentials), and each handler
// enforces exactly what it needs — a browser session for /authorize, PKCE +
// code possession for /token, nothing for discovery.
type Server struct {
	q         db.Querier
	jwtSecret string
	// baseURL is the public origin (config.BaseURL, e.g.
	// https://app.example.com) — issuer identity and the base for every
	// advertised endpoint.
	baseURL string
	// httpClient fetches CIMD documents; overridable in tests.
	httpClient *http.Client
}

func NewServer(q db.Querier, jwtSecret, baseURL string) *Server {
	return &Server{
		q:         q,
		jwtSecret: jwtSecret,
		baseURL:   baseURL,
		httpClient: &http.Client{
			Timeout: cimdFetchTimeout,
			// A CIMD URL is attacker-supplied input; never follow it off-host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			// SSRF guard: refuse to dial non-public addresses (checked at dial
			// time on the resolved IP, so DNS rebinding can't slip past).
			Transport: &http.Transport{DialContext: newCIMDDialContext()},
		},
	}
}
