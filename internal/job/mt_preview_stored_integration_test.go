package job_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The CAT editor sidebar's MT preview must reuse the MT the pipeline already
// produced (translation_history kind='mt') instead of live-calling the engine
// again. With an AI-as-MT engine, a live call is one paid LLM round trip
// per segment click — re-deriving text already on record (and without
// the segment prompt, so possibly a *different* text than the pipeline's).
//
// Anti-tautology RED proof: revert the translation_id branch in HandleMTPreview
// (internal/job/handler_pipeline.go) — the handler ignores the stored history
// row, falls through to the live path, and (no engine configured here) returns
// empty text. The stored-text assertion below flips to fail.
func TestIntegrationMTPreview_ReusesStoredMT(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "MTPreviewStoredCo")
	user := testutil.SeedUser(t, q, tenant.ID, "u@mtpreview.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "MT Preview Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 0)
	tr := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "de")

	storedText := "Hallo Welt"
	if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID: tr.ID,
		Kind:          db.TranslationHistoryKindMt,
		Text:          &storedText,
		Details:       []byte(`{"engine_name":"ai:Fancy LLM"}`),
	}); err != nil {
		t.Fatalf("insert mt history: %v", err)
	}

	t.Run("stored MT is returned without a live engine call", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello%%20world&translation_id=%d", job.ID, tr.ID),
			nil, user.ID, tenant.ID,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Text   string `json:"text"`
			Engine string `json:"engine"`
			Stored bool   `json:"stored"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// No MT engine is configured for this project, so a live fetch can
		// only return empty text — non-empty text proves the stored path.
		if resp.Text != storedText {
			t.Errorf("expected stored MT %q, got %q", storedText, resp.Text)
		}
		if resp.Engine != "ai:Fancy LLM" {
			t.Errorf("expected engine name from history details, got %q", resp.Engine)
		}
		if !resp.Stored {
			t.Errorf("expected stored=true marker")
		}
	})

	t.Run("newest mt history row wins", func(t *testing.T) {
		newerText := "Hallo Welt (v2)"
		if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: tr.ID,
			Kind:          db.TranslationHistoryKindMt,
			Text:          &newerText,
			Details:       []byte(`{"engine_name":"ai:Fancy LLM"}`),
		}); err != nil {
			t.Fatalf("insert second mt history: %v", err)
		}
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello%%20world&translation_id=%d", job.ID, tr.ID),
			nil, user.ID, tenant.ID,
		)
		var resp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Text != newerText {
			t.Errorf("expected newest MT %q, got %q", newerText, resp.Text)
		}
	})

	t.Run("no stored MT falls back to live path", func(t *testing.T) {
		segNoMT := testutil.SeedSegment(t, q, file.ID, "No MT here", 1)
		trNoMT := testutil.SeedTranslation(t, q, segNoMT.ID, "de", db.TranslationStatusNeedsReview)
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=No%%20MT%%20here&translation_id=%d", job.ID, trNoMT.ID),
			nil, user.ID, tenant.ID,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Text   string `json:"text"`
			Stored bool   `json:"stored"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// No engine configured → live path returns empty text, not an error.
		if resp.Text != "" || resp.Stored {
			t.Errorf("expected live-path fallback (empty text, stored=false), got text=%q stored=%v", resp.Text, resp.Stored)
		}
	})

	t.Run("translation_id from another job cannot leak stored text", func(t *testing.T) {
		// Same tenant, different file+job: the stored lookup must be scoped
		// to THIS job's file + target language, so tr.ID resolved against
		// otherJob returns nothing and the endpoint falls back to live.
		otherFile := testutil.SeedFile(t, q, project.ID, "other.json", db.FileFormatJsonI18n)
		otherJob := testutil.SeedJob(t, q, otherFile.ID, "de")
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/mt-preview?source_text=Hello%%20world&translation_id=%d", otherJob.ID, tr.ID),
			nil, user.ID, tenant.ID,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Text == storedText {
			t.Errorf("stored MT leaked across jobs: got %q", resp.Text)
		}
	})
}
