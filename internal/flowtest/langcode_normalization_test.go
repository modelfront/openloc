package flowtest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationLangCodeNormalization verifies that all API endpoints normalize
// language codes before storage and lookup. Sends non-normalized codes (uppercase,
// underscores) and asserts they match normalized data in the DB.
func TestIntegrationLangCodeNormalization(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenant := testutil.SeedTenant(t, q, "NormCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "norm@test.com", "pass")

	// Seed a project with normalized codes
	proj := testutil.SeedProject(t, q, tenant.ID, "NormProject", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, proj.ID, "test.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusNeedsReview)

	// Seed a TM with normalized codes
	tm := testutil.SeedTM(t, q, tenant.ID, "NormTM")
	ctx := context.Background()
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello world", TargetText: "Bonjour le monde", CreatedBy: "seed",
	})
	if err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: proj.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	if _, err := q.CreateComment(ctx, db.CreateCommentParams{
		TranslationID: tr.ID, UserID: &user.ID, Content: "check this",
	}); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	// 2.2: TM listing — query with non-normalized codes, should match normalized DB entries
	t.Run("2.2 TM listing normalizes query params", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/tms/%d/entries?source_language=EN&target_language=FR", tm.ID),
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		body := rec.Body.String()
		if !strings.Contains(body, "Hello world") {
			t.Fatalf("expected TM entry in response, got: %s", body)
		}
	})

	// 2.3: Concordance — query with non-normalized codes
	t.Run("2.3 concordance normalizes language params", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/tms/concordance?project_id=%d&source_language=EN&target_language=FR&q=Hello", proj.ID),
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			TMMatches []struct {
				SourceText string `json:"source_text"`
			} `json:"tm_matches"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if len(resp.TMMatches) != 1 || resp.TMMatches[0].SourceText != "Hello world" {
			t.Fatalf("expected the Hello world TM hit for EN/FR, got: %s", rec.Body.String())
		}
	})

	// 2.4: Translation listing — query with non-normalized lang
	t.Run("2.4 translation listing normalizes lang param", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/files/%d/translations?lang=FR", file.ID),
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		body := rec.Body.String()
		// Should return translation data (target_language: "fr") even though we queried with "FR"
		if !strings.Contains(body, `"target_language":"fr"`) {
			t.Fatalf("expected translation with target_language 'fr' in response, got: %s", body)
		}
	})

	// 2.5: Comment count — query with non-normalized lang
	t.Run("2.5 comment count normalizes lang param", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/files/%d/comment-counts?lang=FR", file.ID),
			nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				TranslationID int64 `json:"translation_id"`
				CommentCount  int   `json:"comment_count"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if len(resp.Data) != 1 || resp.Data[0].TranslationID != tr.ID || resp.Data[0].CommentCount != 1 {
			t.Fatalf("expected 1 comment on translation %d for lang=FR, got: %s", tr.ID, rec.Body.String())
		}
	})
}

// 2.1: TMX import normalization — requires async import, tested separately
func TestIntegrationTMXImportNormalizesLanguageCodes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "TMXCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "tmx@test.com", "pass")
	tm := testutil.SeedTM(t, q, tenant.ID, "ImportTM")

	// TMX with non-normalized language codes (uppercase, as Trados exports)
	tmxContent := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="EN-US" />
  <body>
    <tu>
      <tuv xml:lang="EN-US"><seg>Test segment</seg></tuv>
      <tuv xml:lang="FR-FR"><seg>Segment de test</seg></tuv>
    </tu>
  </body>
</tmx>`

	rec := testutil.DoFileUpload(t, router,
		fmt.Sprintf("/api/tms/%d/import", tm.ID),
		"test.tmx", "application/xml", tmxContent,
		user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusAccepted)

	// Poll for async import completion
	var entries []db.TranslationMemory
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		entries, _ = q.ListTMEntriesCursor(ctx, db.ListTMEntriesCursorParams{
			TmID:           tm.ID,
			SourceLanguage: "",
			TargetLanguage: "",
			EntryLimit:     10,
		})
		if len(entries) > 0 {
			break
		}
	}

	if len(entries) == 0 {
		t.Fatal("TMX import produced no entries after 3s polling")
	}

	// Verify language codes are normalized (lowercase, hyphens)
	for _, e := range entries {
		if e.SourceLanguage != "en-us" {
			t.Errorf("expected source_language 'en-us', got %q", e.SourceLanguage)
		}
		if e.TargetLanguage != "fr-fr" {
			t.Errorf("expected target_language 'fr-fr', got %q", e.TargetLanguage)
		}
	}
}
