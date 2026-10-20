package llmdebug_test

// The debug feed's gate is a security boundary: with neither a dev ring nor a
// reader wired, /api/debug/llm/calls answers 404 exactly like
// /api/auth/dev-login, and the persisted feed is platform-operator only.
// External test package because it builds the full api router (which imports
// llmdebug).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/llmdebug"
	"openloc/internal/testutil"
)

func TestDebugRoute_GatedOnRing(t *testing.T) {
	pool, queries, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	userID, tenantID := testutil.RegisterUser(t, queries, "llmdebug")

	baseDeps := func() api.Deps {
		return api.Deps{
			Queries:   db.New(pool),
			DB:        pool,
			JWTSecret: testutil.TestJWTSecret,
			BaseURL:   "http://localhost:5173",
		}
	}

	// Production shape: no ring ⇒ route absent ⇒ 404 even when authenticated.
	prod := api.NewRouter(baseDeps())
	rec := testutil.DoRequest(t, prod, http.MethodGet, "/api/debug/llm/calls", nil, userID, tenantID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without ring: want 404, got %d", rec.Code)
	}

	// Dev shape: ring wired ⇒ 200 with the feed.
	ring := llmdebug.NewRing(10)
	ring.Record(context.Background(), llmdebug.Entry{Kind: llmdebug.KindMT})
	deps := baseDeps()
	deps.LLMDebugRing = ring
	dev := api.NewRouter(deps)
	rec = testutil.DoRequest(t, dev, http.MethodGet, "/api/debug/llm/calls", nil, userID, tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("with ring: want 200, got %d — body: %s", rec.Code, rec.Body)
	}

	// PRODUCTION-WITH-PERSISTENCE, capture only: a Store is
	// wired but no reader and no ring ⇒ 404. Recording and exposing are separate
	// decisions, and this pins that: route gating keys on LLMDebugRing /
	// LLMDebugReader, capture keys on LLMDebugRecorder.
	persisted := baseDeps()
	persisted.LLMDebugRecorder = llmdebug.NewStore(discardObjectStore{}, llmdebug.StoreOpts{})
	prodPersisting := api.NewRouter(persisted)
	rec = testutil.DoRequest(t, prodPersisting, http.MethodGet, "/api/debug/llm/calls", nil, userID, tenantID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("persisting production without a reader must 404 the viewer: got %d", rec.Code)
	}

	// Unauthenticated must not reach the feed either.
	rec = testutil.DoRequest(t, dev, http.MethodGet, "/api/debug/llm/calls", nil, 0, 0)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: want 401, got %d", rec.Code)
	}
}

// discardObjectStore satisfies the Store's storage seam without touching S3.
type discardObjectStore struct{}

func (discardObjectStore) Put(context.Context, string, io.Reader) error { return nil }

// TestDebugRoute_ProdReaderIsPlatformOwnerOnly covers the production feed, which
// reads persisted objects instead of a ring.
//
// Pins that the feed, which shows every org's segment text because an Entry
// has no tenant_id, is reachable ONLY by a configured platform operator — never
// by a tenant owner or admin, however privileged they are inside their own org.
func TestDebugRoute_ProdReaderIsPlatformOwnerOnly(t *testing.T) {
	pool, queries, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenantOwnerID, tenantID := testutil.RegisterUser(t, queries, "llmdebug-tenant")
	operatorID, operatorTenant := testutil.RegisterUser(t, queries, "llmdebug-operator")

	deps := api.Deps{
		Queries:   db.New(pool),
		DB:        pool,
		JWTSecret: testutil.TestJWTSecret,
		BaseURL:   "http://localhost:5173",
		// testutil.RegisterUser derives the email from the prefix.
		PlatformAdminEmails: []string{"llmdebug-operator@test.com"},
		LLMDebugReader:      llmdebug.NewReader(emptyReaderStore{}, ""),
	}
	router := api.NewRouter(deps)

	// A tenant OWNER — the highest role inside an org — must not reach it.
	rec := testutil.DoRequest(t, router, http.MethodGet, "/api/debug/llm/calls", nil, tenantOwnerID, tenantID)
	if rec.Code != http.StatusForbidden {
		t.Errorf("tenant owner: want 403, got %d — this feed is cross-tenant", rec.Code)
	}

	// Unauthenticated must not reach it either.
	rec = testutil.DoRequest(t, router, http.MethodGet, "/api/debug/llm/calls", nil, 0, 0)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: want 401, got %d", rec.Code)
	}

	// The configured platform operator gets the feed.
	rec = testutil.DoRequest(t, router, http.MethodGet, "/api/debug/llm/calls", nil, operatorID, operatorTenant)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform operator: want 200, got %d — body: %s", rec.Code, rec.Body)
	}

	// With no reader configured the path is absent even for an operator, so
	// enabling persistence is what turns the viewer on — not merely being staff.
	deps.LLMDebugReader = nil
	rec = testutil.DoRequest(t, api.NewRouter(deps), http.MethodGet, "/api/debug/llm/calls", nil, operatorID, operatorTenant)
	if rec.Code != http.StatusNotFound {
		t.Errorf("no reader: want 404 even for an operator, got %d", rec.Code)
	}
}

// emptyReaderStore is a reader-side store with nothing in it.
type emptyReaderStore struct{}

func (emptyReaderStore) ListKeys(context.Context, string) ([]string, error) {
	return nil, nil
}

func (emptyReaderStore) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("empty store")
}
