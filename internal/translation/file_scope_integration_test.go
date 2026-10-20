package translation_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// The /api/files/{fileID} middleware authorises the file in the route, so
// every /translations/{translationID} handler must also check that the
// translation belongs to that file. Otherwise a linguist assigned to file A
// reads and writes file B's segments through A's URL.
func TestIntegrationTranslation_RouteFileMustOwnTranslation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)

	_, tenantID := testutil.RegisterUser(t, q, "xfile-tr")
	project := testutil.SeedProject(t, q, tenantID, "Cross File", "en", []string{"de"})

	fileA := testutil.SeedFile(t, q, project.ID, "a.docx", db.FileFormatDocx)
	segA := testutil.SeedSegment(t, q, fileA.ID, "source a", 1)
	trA := testutil.SeedTranslation(t, q, segA.ID, "de", db.TranslationStatusNeedsReview)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")

	fileB := testutil.SeedFile(t, q, project.ID, "b.docx", db.FileFormatDocx)
	segB := testutil.SeedSegment(t, q, fileB.ID, "source b", 1)
	trB := testutil.SeedTranslation(t, q, segB.ID, "de", db.TranslationStatusNeedsReview)
	testutil.SeedJob(t, q, fileB.ID, "de")

	linguist := testutil.SeedUserWithRole(t, q, tenantID, "xfile-linguist@test.com", "pass", "linguist")
	testutil.SeedJobStep(t, q, project.SettingsProfileID, jobA.ID, &linguist.ID)

	do := func(method, path string, body any) (int, string) {
		rec := testutil.DoRequestWithRole(t, router, method, path, body, linguist.ID, tenantID, "linguist")
		return rec.Code, rec.Body.String()
	}

	// Control: the linguist reaches their own file's translation.
	if code, body := do(http.MethodGet, fmt.Sprintf("/api/files/%d/translations/%d", fileA.ID, trA.ID), nil); code != http.StatusOK {
		t.Fatalf("control GET on own file: want 200, got %d: %s", code, body)
	}

	if code, body := do(http.MethodGet, fmt.Sprintf("/api/files/%d/segments/%d", fileA.ID, segA.ID), nil); code != http.StatusOK {
		t.Fatalf("control GET on own file's segment: want 200, got %d: %s", code, body)
	}

	// Locked, so a cross-file unlock would show.
	if err := q.LockTranslation(testutil.Ctx(), trB.ID); err != nil {
		t.Fatalf("lock: %v", err)
	}
	base := fmt.Sprintf("/api/files/%d/translations/%d", fileA.ID, trB.ID)
	write := map[string]any{"human_text": "should not land"}
	cases := []struct {
		name, method, path string
		body               any
	}{
		{"get", http.MethodGet, base, nil},
		{"confirm", http.MethodPut, base, write},
		{"draft", http.MethodPut, base + "/draft", write},
		{"unlock", http.MethodPost, base + "/unlock", nil},
		{"segment", http.MethodGet, fmt.Sprintf("/api/files/%d/segments/%d", fileA.ID, segB.ID), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := do(tc.method, tc.path, tc.body); code != http.StatusNotFound {
				t.Errorf("%s %s: want 404, got %d: %s", tc.method, tc.path, code, body)
			}
		})
	}

	after, err := q.GetTranslation(testutil.Ctx(), trB.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Locked {
		t.Error("file B's translation was unlocked through file A's URL")
	}
	if after.ActiveText != nil && *after.ActiveText == "should not land" {
		t.Error("file B's translation was written through file A's URL")
	}
	if after.Status != db.TranslationStatusNeedsReview {
		t.Errorf("file B's translation status = %s, want needs_review", after.Status)
	}
}
