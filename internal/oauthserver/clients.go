package oauthserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/netguard"
)

// Client identification, both flavors the directories use:
//
//   - DCR (RFC 7591): the client POSTs /oauth/register once and gets a random
//     client_id. Claude falls back to this; it registers a new client per
//     fresh connection, which is why the directories prefer CIMD.
//   - CIMD: the client_id IS an https URL pointing at a JSON metadata
//     document the client hosts (Claude and ChatGPT both publish one). We
//     fetch it, validate, and cache it as a client row keyed by the URL.
//
// Either way a client is PUBLIC (no secret): possession of a client_id grants
// nothing — security rests on PKCE + exact redirect URI matching.

const (
	// cimdMaxSize caps the metadata document read. A real CIMD is <2KB.
	cimdMaxSize = 64 * 1024
	// cimdFetchTimeout bounds the fetch inside the authorize request.
	cimdFetchTimeout = 5 * time.Second
	// cimdMaxAge is how long a cached CIMD row is trusted before re-fetching.
	cimdMaxAge = 24 * time.Hour
	// maxRedirectURIs bounds registration input.
	maxRedirectURIs = 10
)

var errClientNotFound = errors.New("oauthserver: unknown client")

// clientRecord is the resolved client, whatever its origin.
type clientRecord struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// HandleRegister is RFC 7591 dynamic client registration. Public clients
// only; we accept the minimum viable subset and ignore the rest.
func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, cimdMaxSize)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirectURIs {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "1-10 redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must be https or loopback http")
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if len(name) > 200 {
		name = name[:200]
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		httputil.ServerError(w, "register", "oauth client")
		return
	}
	id := "dcr_" + base64.RawURLEncoding.EncodeToString(buf)
	uris, _ := json.Marshal(req.RedirectURIs)
	if _, err := s.q.CreateOAuthClient(r.Context(), db.CreateOAuthClientParams{
		ID: id, Kind: "dcr", Name: name, RedirectUris: uris,
	}); err != nil {
		logging.Error(r.Context(), err, "oauth client registration failed", "oauth-register-failed")
		httputil.ServerError(w, "register", "oauth client")
		return
	}
	// RFC 7591 response. token_endpoint_auth_method none = public client.
	httputil.JSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_name":                name,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// resolveClient returns the client for a client_id: a DCR row, a cached CIMD
// row, or a freshly fetched CIMD document.
func (s *Server) resolveClient(ctx context.Context, clientID string) (clientRecord, error) {
	row, err := s.q.GetOAuthClient(ctx, clientID)
	switch {
	case err == nil:
		fresh := row.Kind != "cimd" ||
			(row.MetadataFetchedAt.Valid && time.Since(row.MetadataFetchedAt.Time) < cimdMaxAge)
		if fresh {
			var uris []string
			if err := json.Unmarshal(row.RedirectUris, &uris); err != nil {
				return clientRecord{}, fmt.Errorf("oauthserver: corrupt client row: %w", err)
			}
			return clientRecord{ID: row.ID, Name: row.Name, RedirectURIs: uris}, nil
		}
		// Stale CIMD cache: fall through to re-fetch.
	case errors.Is(err, pgx.ErrNoRows):
		// Unknown id: only a CIMD URL can self-introduce.
	default:
		return clientRecord{}, err
	}

	if !isCIMDURL(clientID) {
		return clientRecord{}, errClientNotFound
	}
	return s.fetchCIMD(ctx, clientID)
}

// isCIMDURL: a CIMD client_id is an absolute https URL.
func isCIMDURL(id string) bool {
	u, err := url.Parse(id)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// fetchCIMD retrieves, validates, and caches a Client ID Metadata Document.
func (s *Server) fetchCIMD(ctx context.Context, clientID string) (clientRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, cimdFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return clientRecord{}, errClientNotFound
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return clientRecord{}, fmt.Errorf("oauthserver: cimd fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return clientRecord{}, errClientNotFound
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, cimdMaxSize)).Decode(&doc); err != nil {
		return clientRecord{}, errClientNotFound
	}
	// The document must claim the id it was fetched from — otherwise any URL
	// could impersonate any client.
	if doc.ClientID != clientID || len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > maxRedirectURIs {
		return clientRecord{}, errClientNotFound
	}
	for _, u := range doc.RedirectURIs {
		if !validRedirectURI(u) {
			return clientRecord{}, errClientNotFound
		}
	}
	name := strings.TrimSpace(doc.ClientName)
	if name == "" {
		name = clientID
	}
	if len(name) > 200 {
		name = name[:200]
	}

	uris, _ := json.Marshal(doc.RedirectURIs)
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if _, err := s.q.CreateOAuthClient(ctx, db.CreateOAuthClientParams{
		ID: clientID, Kind: "cimd", Name: name, RedirectUris: uris, MetadataFetchedAt: now,
	}); err != nil {
		return clientRecord{}, err
	}
	return clientRecord{ID: clientID, Name: name, RedirectURIs: doc.RedirectURIs}, nil
}

// validRedirectURI admits https URLs and loopback http (RFC 8252 native
// clients). Plain http to a real host is never acceptable.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return netguard.IsLocalhost(u.Hostname())
	default:
		return false
	}
}

// redirectAllowed does exact matching, EXCEPT loopback redirects where the
// port is ignored: RFC 8252 §7.3 requires port-agnostic matching for
// 127.0.0.1, and Claude Code's CIMD declares localhost the same way.
func redirectAllowed(registered []string, presented string) bool {
	p, err := url.Parse(presented)
	if err != nil {
		return false
	}
	for _, reg := range registered {
		if reg == presented {
			return true
		}
		g, err := url.Parse(reg)
		if err != nil {
			continue
		}
		if g.Scheme == "http" && p.Scheme == "http" &&
			netguard.IsLocalhost(g.Hostname()) && netguard.IsLocalhost(p.Hostname()) &&
			g.Hostname() == p.Hostname() && g.Path == p.Path {
			return true // same loopback host+path, any port
		}
	}
	return false
}

// oauthError writes the RFC 6749 JSON error shape.
func oauthError(w http.ResponseWriter, status int, code, description string) {
	httputil.JSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}
