package oauthserver

import (
	"net/url"
	"strings"
)

// This server issues tokens for exactly one protected resource, the MCP
// endpoint. Clients name it in the RFC 8707 `resource` parameter; any other
// value is refused with invalid_target, so no token is ever minted for an
// audience it can't be used at. An absent `resource` is accepted for clients
// that predate RFC 8707 — the token is still only valid on /mcp.

// Resource is the canonical resource identifier, as advertised in the
// protected-resource metadata.
func (s *Server) Resource() string { return s.baseURL + "/mcp" }

// ResourceMetadataURL is where the RFC 9728 protected-resource metadata lives.
func (s *Server) ResourceMetadataURL() string {
	return s.baseURL + "/.well-known/oauth-protected-resource"
}

// isOurResource compares v to Resource() the way URIs compare: scheme and
// host case-insensitively, the path exactly bar a trailing slash. Queries and
// fragments are never part of our identifier.
func (s *Server) isOurResource(v string) bool {
	got, err := url.Parse(v)
	if err != nil {
		return false
	}
	want, err := url.Parse(s.Resource())
	if err != nil {
		return false
	}
	return strings.EqualFold(got.Scheme, want.Scheme) &&
		strings.EqualFold(got.Host, want.Host) &&
		strings.TrimSuffix(got.Path, "/") == want.Path &&
		got.RawQuery == "" && got.Fragment == ""
}

// bearerChallenge builds a WWW-Authenticate value pointing at our metadata,
// with optional extra auth-params.
func (s *Server) bearerChallenge(params ...string) string {
	return strings.Join(append([]string{`Bearer resource_metadata="` + s.ResourceMetadataURL() + `"`}, params...), ", ")
}

// WriteScopeChallenge is the step-up challenge for a read-scoped token that
// called a write tool: clients that understand it re-authorize asking for
// write access.
func (s *Server) WriteScopeChallenge() string {
	return s.bearerChallenge(
		`error="insufficient_scope"`,
		`scope="write"`,
		`error_description="This action needs write access. Reconnect and approve write access."`,
	)
}
