package qa_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Run QA on a selection checks exactly the selected rows of the job: a
// selected row with a dropped number gets its finding, a selected clean row
// loses a stale one, and rows outside the selection or outside the job — even
// when their id is sent — keep what they had.
func TestIntegration_RunQASelection_OnlySelectedJobRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	user, tenant := testutil.RegisterUser(t, q, "qasel")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "QA Selection", "source_language": "en", "target_languages": []string{"es", "fr"},
	}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var idResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &idResp)
	projectID := idResp.Data.ID
	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles",
		map[string]string{"name": "QA Selection profile"}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	testutil.DecodeJSON(t, rec, &idResp)
	profileID := idResp.Data.ID
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/qa-profiles/%d/rules", profileID),
		map[string]any{"check_type": "number_consistency", "enabled": true, "severity": "error"}, user, tenant), http.StatusOK)
	testutil.AssertStatus(t, testutil.DoRequest(t, router, "PATCH", fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profileID}, user, tenant), http.StatusOK)

	file := testutil.SeedFile(t, q, projectID, "sel.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")
	testutil.SeedJob(t, q, file.ID, "fr")
	row := func(fileID int64, lang, src, tgt string, pos int32) int64 {
		seg := testutil.SeedSegment(t, q, fileID, src, pos)
		tr := testutil.SeedTranslation(t, q, seg.ID, lang, db.TranslationStatusNeedsReview)
		if _, err := pool.Exec(ctx, "UPDATE translations SET active_text = $2 WHERE id = $1", tr.ID, tgt); err != nil {
			t.Fatal(err)
		}
		return tr.ID
	}
	selectedBad := row(file.ID, "es", "There are 42 items", "Hay cosas", 1)
	selectedGood := row(file.ID, "es", "Nothing numeric", "Nada", 2)
	unselectedBad := row(file.ID, "es", "Call 911", "Llama", 3)
	segs, err := q.ListSegmentsByFile(ctx, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	frBad := testutil.SeedTranslation(t, q, segs[0].ID, "fr", db.TranslationStatusNeedsReview).ID
	if _, err := pool.Exec(ctx, "UPDATE translations SET active_text = 'Il y a des choses' WHERE id = $1", frBad); err != nil {
		t.Fatal(err)
	}
	// A stale finding on the clean selected row, from text since corrected.
	if _, err := pool.Exec(ctx, `INSERT INTO qa_results (translation_id, check_type, severity, message, ignored)
		VALUES ($1, 'number_consistency', 'error', 'old', false)`, selectedGood); err != nil {
		t.Fatal(err)
	}

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID),
		map[string]any{"translation_ids": []int64{selectedBad, selectedGood, frBad}}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var runResp struct {
		Data struct {
			Issues int `json:"issues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runResp); err != nil {
		t.Fatal(err)
	}
	if runResp.Data.Issues != 1 {
		t.Errorf("issues = %d, want 1 (the selected row with the dropped number)", runResp.Data.Issues)
	}

	count := func(id int64) int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM qa_results WHERE translation_id = $1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for id, want := range map[int64]int{selectedBad: 1, selectedGood: 0, unselectedBad: 0, frBad: 0} {
		if got := count(id); got != want {
			t.Errorf("row %d: %d findings, want %d", id, got, want)
		}
	}

	rec = testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID),
		map[string]any{"translation_ids": []int64{}}, user, tenant)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}
