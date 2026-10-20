package translation_test

import (
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/segmentbulk"
	"openloc/internal/testutil"
)

// "Approve all" confirms a job's rows in a few statements per batch: the
// statement count must not grow with the number of rows, while every row is
// still approved, recorded in history and written to TM.
func TestIntegrationConfirmAll_StatementsDoNotGrowWithRows(t *testing.T) {
	run := func(extra int) (statements int, f *confirmFixture) {
		t.Helper()
		f = newConfirmFixture(t)
		countPool, log := testutil.CountingPool(t)
		router := testutil.NewTestRouter(countPool)
		testutil.SeedSegmentsBulk(t, f.pool, f.job.FileID, "es", 100, extra, "Texto")
		log.Reset()
		rec := testutil.DoRequestWithRole(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/translations/confirm-all", f.job.ID), nil, f.userID, f.tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		res := decodeResult(t, rec)
		if len(res.Applied) != extra+1 { // the bulk rows and the fixture's draft
			t.Fatalf("applied %d rows, want %d", len(res.Applied), extra+1)
		}
		return log.Total(), f
	}

	small, _ := run(20)
	large, f := run(320)
	if extra := large - small; extra > 60 {
		t.Errorf("320 rows sent %d statements, 20 rows %d: %d more for 300 rows, want at most 60", large, small, extra)
	}

	approved := testutil.CountRows(t, f.pool, `
		SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.status = 'approved' AND t.active_text = 'Texto' AND t.edited_by = $2`, f.job.FileID, f.userID)
	history := testutil.CountRows(t, f.pool, `
		SELECT count(*) FROM translation_history th JOIN translations t ON t.id = th.translation_id
		JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.active_text = 'Texto' AND th.created_by = $2 AND th.kind IN ('human', 'approved')`, f.job.FileID, f.userID)
	inTM := testutil.CountRows(t, f.pool, `SELECT count(*) FROM translation_memory WHERE tm_id = $1 AND target_text = 'Texto'`, f.tmID)
	if approved != 320 || history != 320 || inTM != 320 {
		t.Errorf("approved %d, history rows %d, TM entries %d; want 320 of each", approved, history, inTM)
	}
}

// "Approve all" goes through the job in batches; the job's QA scope (project,
// rules, glossary) is loaded once for the whole run, not once per batch.
func TestIntegrationConfirmAll_LoadsTheQAScopeOnce(t *testing.T) {
	f := newConfirmFixture(t)
	countPool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(countPool)
	testutil.SeedSegmentsBulk(t, f.pool, f.job.FileID, "es", 100, 2*segmentbulk.MaxIDs+1, "Texto")
	log.Reset()
	rec := testutil.DoRequestWithRole(t, router, "POST",
		fmt.Sprintf("/api/jobs/%d/translations/confirm-all", f.job.ID), nil, f.userID, f.tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if n := log.Count("ListQARules"); n != 1 {
		t.Errorf("a three-batch approve-all loaded the QA rules %d times, want once", n)
	}
}
