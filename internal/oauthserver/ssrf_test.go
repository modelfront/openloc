package oauthserver

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// The DEFAULT server's CIMD http client must actually be wired to the guarded
// dialer — ssrfSafeDialer working in isolation proves nothing if server.go
// drops the Transport. A loopback client_id must be refused with the guard's
// message (not a plain connection error).
func TestServerCIMDClientUsesGuardedDialer(t *testing.T) {
	srv := NewServer(cimdStubQ{}, "secret", "https://app.example.com")
	_, err := srv.resolveClient(context.Background(), "https://127.0.0.1:1/client-metadata.json")
	if err == nil {
		t.Fatal("resolveClient reached a loopback CIMD URL — SSRF guard not wired into Server.httpClient")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("expected the guard's refusal, got a different error (guard bypassed?): %v", err)
	}
}

// cimdStubQ satisfies db.Querier just enough for resolveClient: an unknown
// client_id (ErrNoRows) so it falls through to the CIMD fetch path.
type cimdStubQ struct{ db.Querier }

func (cimdStubQ) GetOAuthClient(context.Context, string) (db.OauthClient, error) {
	return db.OauthClient{}, pgx.ErrNoRows
}
