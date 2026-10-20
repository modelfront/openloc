package translation_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A file's jobs are one per target language, often held by different
// vendors. A linguist assigned to the DE job of a file reaches the file's
// source and the DE rows, never the FR rows of the same file.
func TestIntegrationTranslation_LinguistLanguageScope(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)

	_, tenantID := testutil.RegisterUser(t, q, "xlang-tr")
	project := testutil.SeedProject(t, q, tenantID, "Cross Lang", "en", []string{"de", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "a.docx", db.FileFormatDocx)
	seg := testutil.SeedSegment(t, q, file.ID, "same source", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "same source", 2)
	trDE := testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusNeedsReview)
	trFR := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusNeedsReview)
	trFRLocked := testutil.SeedTranslation(t, q, seg2.ID, "fr", db.TranslationStatusNeedsReview)
	jobDE := testutil.SeedJob(t, q, file.ID, "de")
	jobFR := testutil.SeedJob(t, q, file.ID, "fr")

	linguist := testutil.SeedUserWithRole(t, q, tenantID, "xlang-de@test.com", "pass", "linguist")
	other := testutil.SeedUserWithRole(t, q, tenantID, "xlang-fr@test.com", "pass", "linguist")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, jobDE.ID, &linguist.ID)
	testutil.SeedJobStep(t, q, project.SettingsProfileID, jobFR.ID, &other.ID)

	if err := q.LockTranslation(testutil.Ctx(), trFRLocked.ID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	do := func(method, path string, body any) (int, string) {
		rec := testutil.DoRequestWithRole(t, router, method, path, body, linguist.ID, tenantID, "linguist")
		return rec.Code, rec.Body.String()
	}
	files := fmt.Sprintf("/api/files/%d", file.ID)

	// Control: the DE job's own rows and the shared source stay reachable.
	for _, path := range []string{
		files + "/segments",
		files + "/translations?lang=de",
		fmt.Sprintf("%s/translations/%d", files, trDE.ID),
		files + "/comment-counts?lang=de",
		fmt.Sprintf("/api/translations/%d/comments", trDE.ID),
		fmt.Sprintf("/api/translations/%d/history", trDE.ID),
	} {
		if code, body := do(http.MethodGet, path, nil); code != http.StatusOK {
			t.Fatalf("control GET %s: want 200, got %d: %s", path, code, body)
		}
	}

	write := map[string]any{"human_text": "should not land"}
	fr := fmt.Sprintf("%s/translations/%d", files, trFR.ID)
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"list", http.MethodGet, files + "/translations?lang=fr", nil},
		{"get", http.MethodGet, fr, nil},
		{"confirm", http.MethodPut, fr, write},
		{"draft", http.MethodPut, fr + "/draft", write},
		{"clear", http.MethodPost, fr + "/clear", nil},
		{"unlock", http.MethodPost, fmt.Sprintf("%s/translations/%d/unlock", files, trFRLocked.ID), nil},
		{"propagate", http.MethodPost, files + "/propagate", map[string]any{
			"source_text": "same source", "target_language": "fr", "human_text": "should not land",
		}},
		{"export", http.MethodGet, files + "/export?lang=fr", nil},
		{"export-xliff", http.MethodGet, files + "/export?lang=fr&format=xliff", nil},
		{"comment-counts", http.MethodGet, files + "/comment-counts?lang=fr", nil},
		{"comments", http.MethodGet, fmt.Sprintf("/api/translations/%d/comments", trFR.ID), nil},
		{"comment-post", http.MethodPost, fmt.Sprintf("/api/translations/%d/comments", trFR.ID), map[string]any{"body": "hi"}},
		{"qa", http.MethodGet, fmt.Sprintf("/api/translations/%d/qa", trFR.ID), nil},
		{"history", http.MethodGet, fmt.Sprintf("/api/translations/%d/history", trFR.ID), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := do(tc.method, tc.path, tc.body); code != http.StatusForbidden {
				t.Errorf("%s %s: want 403, got %d: %s", tc.method, tc.path, code, body)
			}
		})
	}

	t.Run("batch-draft", func(t *testing.T) {
		code, body := do(http.MethodPut, files+"/translations/batch-draft", map[string]any{
			"fixes": []map[string]any{{"translation_id": trFR.ID, "human_text": "should not land"}},
		})
		if code != http.StatusOK {
			t.Fatalf("batch-draft: want 200, got %d: %s", code, body)
		}
		if !strings.Contains(body, fmt.Sprintf(`"rejected":[%d]`, trFR.ID)) {
			t.Errorf("batch-draft: want FR row rejected, got %s", body)
		}
	})

	after, err := q.GetTranslation(testutil.Ctx(), trFR.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.ActiveText != nil && *after.ActiveText == "should not land" {
		t.Error("the FR translation was written by the DE linguist")
	}
	if after.Status != db.TranslationStatusNeedsReview {
		t.Errorf("FR status = %s, want needs_review", after.Status)
	}
	locked, err := q.GetTranslation(testutil.Ctx(), trFRLocked.ID)
	if err != nil {
		t.Fatalf("reload locked: %v", err)
	}
	if !locked.Locked {
		t.Error("the FR translation was unlocked by the DE linguist")
	}
	if locked.ActiveText != nil && *locked.ActiveText == "should not land" {
		t.Error("the locked FR translation was written by propagation")
	}
}

// Propagation writes the language its gate checked: a target_language the
// client sends in another form (`ES`) is normalized before both, so it never
// reaches a row stored under that raw form.
func TestIntegrationTranslation_PropagateWritesTheCheckedLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)

	_, tenantID := testutil.RegisterUser(t, q, "xlang-prop")
	project := testutil.SeedProject(t, q, tenantID, "Prop Lang", "en", []string{"es"})
	file := testutil.SeedFile(t, q, project.ID, "p.docx", db.FileFormatDocx)
	seg := testutil.SeedSegment(t, q, file.ID, "same source", 1)
	raw := testutil.SeedTranslation(t, q, seg.ID, "ES", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")
	linguist := testutil.SeedUserWithRole(t, q, tenantID, "xlang-prop-ling@test.com", "pass", "linguist")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &linguist.ID)

	rec := testutil.DoRequestWithRole(t, router, http.MethodPost, fmt.Sprintf("/api/files/%d/propagate", file.ID),
		map[string]any{"source_text": "same source", "target_language": "ES", "human_text": "should not land"},
		linguist.ID, tenantID, "linguist")
	if rec.Code != http.StatusOK {
		t.Fatalf("propagate: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	after, err := q.GetTranslation(testutil.Ctx(), raw.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.ActiveText != nil && *after.ActiveText == "should not land" {
		t.Error("propagation wrote a row stored under the raw language form the gate never checked")
	}
}
