package oauthserver

import (
	"encoding/json"
	"net/http"

	"openloc/internal/httputil"
)

// Discovery documents. Claude and ChatGPT find everything from these two:
// the protected-resource metadata (RFC 9728) names the authorization server,
// whose metadata (RFC 8414) names the endpoints. Both are static given
// baseURL, cache-friendly, and public by design.

// HandleAuthServerMetadata serves /.well-known/oauth-authorization-server.
func (s *Server) HandleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httputil.JSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.baseURL,
		"authorization_endpoint":                s.baseURL + "/oauth/authorize",
		"token_endpoint":                        s.baseURL + "/oauth/token",
		"registration_endpoint":                 s.baseURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"read", "write"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		// Claude selects CIMD only when BOTH this flag and "none" above are
		// advertised — CIMD avoids the client-per-connection sprawl DCR causes.
		"client_id_metadata_document_supported": true,
		// RFC 9207: every authorization response carries iss, which lets ChatGPT
		// use its one stable redirect URI.
		"authorization_response_iss_parameter_supported": true,
	})
}

// HandleProtectedResourceMetadata serves /.well-known/oauth-protected-resource
// (and its /mcp path-suffix variant, which some clients probe). The resource
// value must match the MCP URL exactly as users enter it in Claude/ChatGPT.
func (s *Server) HandleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httputil.JSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Resource(),
		"authorization_servers":    []string{s.baseURL},
		"scopes_supported":         []string{"read", "write"},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   s.baseURL + "/docs/guides/mcp",
	})
}

// WWWAuthenticate401 wraps a handler chain so any 401 it emits carries the
// RFC 9728 pointer — the handshake through which Claude/ChatGPT discover our
// authorization server. Claude only honors the header on a 401, never on 200.
func (s *Server) WWWAuthenticate401(next http.Handler) http.Handler {
	header := s.bearerChallenge()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&wwwAuthWriter{ResponseWriter: w, header: header}, r)
	})
}

type wwwAuthWriter struct {
	http.ResponseWriter
	header string
	wrote  bool
}

func (w *wwwAuthWriter) WriteHeader(status int) {
	if !w.wrote {
		w.wrote = true
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", w.header)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

// Flush keeps SSE streaming working through the wrapper (the MCP transport
// streams responses).
func (w *wwwAuthWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// writeJSON writes a JSON body with the given status. Same shape as
// httputil.JSON — a local copy only so this file stays dependency-light; not a
// behavioral difference.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
