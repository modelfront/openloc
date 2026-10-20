package testutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/ai"
	"openloc/internal/api"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/hold"
	"openloc/internal/oauthserver"
	"openloc/internal/productdocs"
	"openloc/internal/publicapi"
)

// TestJWTSecret is the fixed JWT secret used in integration tests.
const TestJWTSecret = "test-secret-do-not-use-in-production"

// TestAISecretsKey encrypts provider API keys in tests. Shared so the router
// and the model catalog derive the same AES key — different keys would make
// every stored token undecryptable and every model list silently empty.
const TestAISecretsKey = "test-ai-secrets-key"

// TestJWT generates a valid JWT for the given user and tenant.
// Defaults to role "owner" for backwards compatibility with existing tests.
func TestJWT(userID, tenantID int64) string {
	return TestJWTWithRole(userID, tenantID, "owner")
}

// TestJWTWithRole generates a valid JWT with a specific role.
func TestJWTWithRole(userID, tenantID int64, role string) string {
	token, err := auth.GenerateToken(userID, tenantID, role, TestJWTSecret, time.Hour)
	if err != nil {
		panic("testutil: failed to generate test JWT: " + err.Error())
	}
	return token
}

// AuthHeader returns a Bearer token header value for the given user and tenant.
func AuthHeader(userID, tenantID int64) string {
	return "Bearer " + TestJWT(userID, tenantID)
}

// IdentityJWT generates an identity-scoped token (no org selected) — the shape
// a user holds before/without an org, used by the org picker + self-serve
// signup.
func IdentityJWT(userID int64) string {
	token, err := auth.GenerateIdentityToken(userID, TestJWTSecret)
	if err != nil {
		panic("testutil: failed to generate identity JWT: " + err.Error())
	}
	return token
}

// IdentityAuthHeader returns a Bearer header carrying an identity-scoped token.
func IdentityAuthHeader(userID int64) string {
	return "Bearer " + IdentityJWT(userID)
}

// AuthHeaderWithRole returns a Bearer token header with a specific role.
func AuthHeaderWithRole(userID, tenantID int64, role string) string {
	return "Bearer " + TestJWTWithRole(userID, tenantID, role)
}

// SeedAPIToken creates a personal access token for the given user in
// the given org and returns the PLAINTEXT token, which is the only thing a
// client can present. Mirrors what HandleCreateAPIToken does, so tests exercise
// the same hash-and-store path the product uses.
//
// capability is "read" or "write"; lifetime is a positive duration (pass a
// negative one to seed an already-expired token).
func SeedAPIToken(t testing.TB, q db.Querier, tenantID, userID int64, capability string, lifetime time.Duration) string {
	t.Helper()
	raw, hash, prefix, err := auth.GeneratePAT()
	if err != nil {
		t.Fatalf("testutil: generate PAT: %v", err)
	}
	if _, err := q.CreateAPIToken(context.Background(), db.CreateAPITokenParams{
		TenantID:   tenantID,
		UserID:     userID,
		Name:       "test-token",
		TokenHash:  hash,
		Prefix:     prefix,
		Capability: capability,
		ExpiresAt:  pgtype.Timestamptz{Time: time.Now().Add(lifetime), Valid: true},
	}); err != nil {
		t.Fatalf("testutil: create api token: %v", err)
	}
	return raw
}

// SeedOAuthToken stores an OAuth grant for (tenant, user) with the given
// scope ("read" | "write") from a client named "ChatGPT", and returns the raw
// access token — the credential Claude or ChatGPT presents on /mcp.
func SeedOAuthToken(t testing.TB, q db.Querier, tenantID, userID int64, scope string) string {
	t.Helper()
	ctx := context.Background()
	client, err := q.CreateOAuthClient(ctx, db.CreateOAuthClientParams{
		ID: "dcr_" + uuid.NewString(), Kind: "dcr", Name: "ChatGPT",
		RedirectUris: []byte(`["https://chatgpt.com/connector_platform_oauth_redirect"]`),
	})
	if err != nil {
		t.Fatalf("testutil: create oauth client: %v", err)
	}
	access, accessHash, err := oauthserver.GenerateAccessToken()
	if err != nil {
		t.Fatalf("testutil: generate oauth token: %v", err)
	}
	now := time.Now()
	if _, err := q.CreateOAuthToken(ctx, db.CreateOAuthTokenParams{
		FamilyID:         pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ClientID:         client.ID,
		UserID:           userID,
		TenantID:         tenantID,
		Scope:            scope,
		AccessTokenHash:  accessHash,
		RefreshTokenHash: "seed-refresh-" + uuid.NewString(),
		AccessExpiresAt:  pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("testutil: create oauth token: %v", err)
	}
	return access
}

// NewTestRouter creates a full API router backed by a real database.
// MT engines are resolved per-project through the profile's MT router — tests
// that exercise the MT phase must point the profile at one via SeedMTEngine +
// SeedMTRouterForProfile (see seedTranslateContext for the canonical
// pattern). nil Storage, nil Enqueuer. File-upload pre-translate is silently
// skipped when Enqueuer is nil — tests that need to observe the upload
// pipeline should use NewTestRouterWithEnqueuer.
func NewTestRouter(pool *pgxpool.Pool) chi.Router {
	return NewTestRouterWithEnqueuer(pool, nil)
}

// NewTestRouterWithEnqueuer is the same as NewTestRouter but wires a non-nil
// TaskEnqueuer. Pass a recording stub to assert what the upload hook (or any
// other enqueueing path) tries to schedule.
func NewTestRouterWithEnqueuer(pool *pgxpool.Pool, enqueuer api.TaskEnqueuer) chi.Router {
	return NewTestRouterWithSpellcheck(pool, enqueuer, "")
}

// NewTestRouterWithSpellcheck points the spell check handler at a specific
// LanguageTool URL — normally an httptest stub. Real LanguageTool responses
// depend on the server's rule set and dictionary version, so asserting against
// the live service would make tests flaky for reasons unrelated to our code.
// An empty ltURL leaves spell check pointed at nothing, which is correct for
// the tests that never call it.
func NewTestRouterWithSpellcheck(pool *pgxpool.Pool, enqueuer api.TaskEnqueuer, ltURL string) chi.Router {
	return NewTestRouterWithCatalog(pool, enqueuer, ltURL, TestAICatalog(pool))
}

// NewTestRouterWithCatalog points the AI model catalog at a specific provider
// double — normally one that fails, so a test can assert what the API reports
// when a vendor call goes wrong.
func NewTestRouterWithCatalog(pool *pgxpool.Pool, enqueuer api.TaskEnqueuer, ltURL string, catalog *ai.Catalog) chi.Router {
	return api.NewRouter(testDeps(pool, enqueuer, ltURL, catalog, nil))
}

// NewTestRouterWithStorage keeps uploaded sources in memory. Publishing a
// draft reads them back to create its jobs, which the default nil store
// cannot serve.
func NewTestRouterWithStorage(pool *pgxpool.Pool, enqueuer api.TaskEnqueuer, store file.BlobStore) chi.Router {
	return api.NewRouter(testDeps(pool, enqueuer, "", TestAICatalog(pool), store))
}

// NewTestRouterWithHoldFeed is NewTestRouter with the segment hold stream
// on, ending when draining closes (nil never drains). The caller runs a
// pglisten.Listener the feed listens on.
func NewTestRouterWithHoldFeed(pool *pgxpool.Pool, feed *hold.Feed, draining <-chan struct{}) chi.Router {
	deps := testDeps(pool, nil, "", TestAICatalog(pool), nil)
	deps.HoldFeed = feed
	deps.Draining = draining
	return api.NewRouter(deps)
}

// NewTestRouterWithoutDemoOrg is NewTestRouter with the Playground off
// (DEMO_ORG=false), as in production by default.
func NewTestRouterWithoutDemoOrg(pool *pgxpool.Pool) chi.Router {
	deps := testDeps(pool, nil, "", TestAICatalog(pool), nil)
	deps.DemoOrg = false
	return api.NewRouter(deps)
}

func testDeps(pool *pgxpool.Pool, enqueuer api.TaskEnqueuer, ltURL string, catalog *ai.Catalog, store file.BlobStore) api.Deps {
	return api.Deps{
		AICatalog:       catalog,
		Queries:         db.New(pool),
		DB:              pool,
		Storage:         store,
		Enqueuer:        enqueuer,
		JWTSecret:       TestJWTSecret,
		BaseURL:         "http://localhost:5173",
		DevAutoLogin:    true,
		AISecretsKey:    TestAISecretsKey,
		SpellcheckLTURL: ltURL,
		DemoOrg:         true,
		Docs:            docsIndex(),
		APIDocs:         apiDocsIndex(),
		APISpec:         APISpec(),
	}
}

// FakeModelLister answers the vendor model-list call with a fixed catalog, so
// tests exercise the real classify/cache path without reaching a provider. The
// ids are real ones spanning both parameter families — a test that pins
// "gpt-4o" or "claude-opus-5" is asserting against something the classifier
// treats exactly as it treats the live response.
type FakeModelLister struct{}

func (FakeModelLister) List(_ context.Context, provider, _, _ string) ([]ai.RemoteModel, error) {
	switch provider {
	case ai.ProviderOpenAI:
		return []ai.RemoteModel{{ID: "gpt-5"}, {ID: "gpt-5-mini"}, {ID: "gpt-4o"}, {ID: "gpt-4o-mini"}, {ID: "gpt-4-turbo"}}, nil
	case ai.ProviderClaude:
		return []ai.RemoteModel{
			{ID: "claude-opus-5", DisplayName: "Opus 5"},
			{ID: "claude-sonnet-5", DisplayName: "Sonnet 5"},
			{ID: "claude-haiku-4-5-20251001", DisplayName: "Haiku 4.5"},
			{ID: "claude-fable-5", DisplayName: "Fable 5"},
		}, nil
	}
	return nil, nil
}

// FakeKeyLoader reports a usable credential for every token row the tenant has
// at a vendor, without decrypting any of them. Seeded tokens carry opaque
// placeholder ciphertext, so the real loader would reject them — and "which keys
// does this tenant have" is all the catalog needs to answer.
type FakeKeyLoader struct{ q db.Querier }

func (l FakeKeyLoader) KeysFor(ctx context.Context, tenantID int64, provider string) ([]ai.Credential, error) {
	rows, err := l.q.ListVendorTokenKeys(ctx, db.ListVendorTokenKeysParams{TenantID: tenantID, Provider: provider})
	if err != nil || len(rows) == 0 {
		return nil, ai.ErrNoCredential
	}
	out := make([]ai.Credential, len(rows))
	for i, row := range rows {
		out[i] = ai.Credential{TokenID: row.ID, APIKey: "test-key", BaseURL: row.BaseUrl}
	}
	return out, nil
}

// TestAICatalog is the model catalog the test router wires: real cache and
// classifier, fake provider. A tenant that connected a key sees models; one
// that hasn't sees none, the same as production.
func TestAICatalog(pool *pgxpool.Pool) *ai.Catalog {
	return TestAICatalogWith(pool, FakeModelLister{})
}

// TestAICatalogWith is TestAICatalog with a caller-supplied provider double.
func TestAICatalogWith(pool *pgxpool.Pool, lister ai.ModelLister) *ai.Catalog {
	return ai.NewCatalog(lister, FakeKeyLoader{q: db.New(pool)}, ai.DefaultModelLimit)
}

// docsIndex parses the embedded user documentation once for every test router.
// The content is static, so a parse failure is a build-time mistake — panic
// rather than hand every test a nil index that mounts nothing.
func docsIndex() *productdocs.Index {
	docsOnce.Do(func() {
		idx, err := productdocs.Default()
		if err != nil {
			panic("testutil: embedded user documentation failed to parse: " + err.Error())
		}
		docsIdx = idx
	})
	return docsIdx
}

// apiDocsIndex is docsIndex for the API guides.
func apiDocsIndex() *productdocs.Index {
	apiDocsOnce.Do(func() {
		idx, err := productdocs.API()
		if err != nil {
			panic("testutil: embedded API guides failed to parse: " + err.Error())
		}
		apiDocsIdx = idx
	})
	return apiDocsIdx
}

// APISpec loads the customer API's OpenAPI document once for every test
// router and spec test.
func APISpec() *publicapi.Spec {
	apiSpecOnce.Do(func() {
		spec, err := publicapi.LoadSpec()
		if err != nil {
			panic("testutil: API specification failed to load: " + err.Error())
		}
		apiSpecVal = spec
	})
	return apiSpecVal
}

var (
	apiSpecOnce sync.Once
	apiSpecVal  *publicapi.Spec
	docsOnce    sync.Once
	docsIdx     *productdocs.Index
	apiDocsOnce sync.Once
	apiDocsIdx  *productdocs.Index
)
