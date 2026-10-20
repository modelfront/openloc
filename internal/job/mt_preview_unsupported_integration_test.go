package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// MTPreview refuses a pair the engine's catalog does not list, and the refusal
// has to survive the handler: it is built as a *httputil.ClientError so the
// sidebar can show which pair failed. Without the AsClientError branch in
// HandleMTPreview the message is swallowed into a generic 500, which is the
// whole point of naming the pair.
//
// Anti-tautology RED proof: drop that branch and this test sees 500 with
// "Internal Server Error" instead of 400 with the language in it.
func TestIntegrationMTPreview_UnsupportedPairIsAClientError(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// An engine that publishes en↔de and nothing else.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]},{"code":"de","name":"German","targets":["en"]}]`)
	}))
	defer catalog.Close()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MTPreviewUnsupportedCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@mtpreview-unsupported.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Thai Project", "en", []string{"th"})
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	testutil.SeedTranslation(t, q, seg.ID, "th", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "th")

	url := catalog.URL
	engine, err := q.CreateMTEngine(ctx, db.CreateMTEngineParams{
		Name: "Catalog engine", EngineType: "libretranslate", ApiUrl: &url,
		Enabled: true, TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	testutil.SeedMTRouterForProfile(t, q, tenant.ID, project.SettingsProfileID, &engine.ID, nil)

	// No translation_id: the handler takes the live path rather than replaying
	// a stored MT row.
	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello%%20world", job.ID),
		nil, user.ID, tenant.ID,
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	// Not just "th": the message contains "the", so a bare substring check passes
	// with both codes stripped.
	if !strings.Contains(resp.Error.Message, "en \u2192 th") {
		t.Errorf("message should name the pair: %q", resp.Error.Message)
	}
}
