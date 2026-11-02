package job_test

import (
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// The bulk bar's Apply TM / Apply MT: one POST /pretranslate with job_ids and
// a phase queues one worker task per selected job, with only that phase. A
// job of another project named in job_ids is skipped. No body keeps the
// Analysis tab's behaviour: every job, TM and MT.
func TestIntegrationProjectPretranslate_SelectionAndPhase(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	enq := &testutil.RecordingEnqueuer{}
	router := testutil.NewTestRouterWithEnqueuer(pool, enq)

	tenant := testutil.SeedTenant(t, q, "PretranslateSelCo")
	user := testutil.SeedUser(t, q, tenant.ID, "pm@pretranslatesel.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Sel", "en", []string{"es", "fr"})
	file := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	es := testutil.SeedJob(t, q, file.ID, "es")
	fr := testutil.SeedJob(t, q, file.ID, "fr")
	other := testutil.SeedProject(t, q, tenant.ID, "Other", "en", []string{"es"})
	foreign := testutil.SeedJob(t, q, testutil.SeedFile(t, q, other.ID, "b.txt", db.FileFormatTxt).ID, "es")

	type result struct {
		Queued  int `json:"queued"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	}
	post := func(t *testing.T, body any) (int, result) {
		t.Helper()
		enq.Pipelines = nil
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/projects/%d/pretranslate", project.ID), body, user.ID, tenant.ID)
		var r result
		if rec.Code < 300 {
			testutil.DecodeJSON(t, rec, &r)
		}
		return rec.Code, r
	}
	queued := func() map[int64]pipeline.TranslatePhases {
		out := map[int64]pipeline.TranslatePhases{}
		for _, c := range enq.Pipelines {
			out[c.JobID] = c.Phases
		}
		return out
	}

	t.Run("tm on one selected job", func(t *testing.T) {
		code, r := post(t, map[string]any{"job_ids": []int64{es.ID}, "phase": "tm"})
		if code != http.StatusAccepted || r.Queued != 1 || r.Total != 1 {
			t.Fatalf("status %d, %+v; want 202 with 1 queued of 1", code, r)
		}
		if got := queued(); len(got) != 1 || !sameTMMT(got[es.ID], true, false, true) {
			t.Errorf("queued %v, want only job %d with TM", got, es.ID)
		}
	})

	t.Run("mt, and a job of another project is skipped", func(t *testing.T) {
		resetJobs(t, q, es.ID, fr.ID)
		code, r := post(t, map[string]any{"job_ids": []int64{es.ID, fr.ID, foreign.ID}, "phase": "mt"})
		if code != http.StatusAccepted || r.Queued != 2 || r.Skipped != 1 || r.Total != 3 {
			t.Fatalf("status %d, %+v; want 202 with 2 queued, 1 skipped of 3", code, r)
		}
		got := queued()
		if len(got) != 2 || !sameTMMT(got[es.ID], false, true, true) || !sameTMMT(got[fr.ID], false, true, true) {
			t.Errorf("queued %v, want jobs %d and %d with MT", got, es.ID, fr.ID)
		}
	})

	t.Run("no body queues every job with TM and MT", func(t *testing.T) {
		resetJobs(t, q, es.ID, fr.ID)
		code, r := post(t, nil)
		if code != http.StatusAccepted || r.Queued != 2 {
			t.Fatalf("status %d, %+v; want 202 with 2 queued", code, r)
		}
		for id, ph := range queued() {
			if !sameTMMT(ph, true, true, false) {
				t.Errorf("job %d queued with %+v, want TM and MT", id, ph)
			}
		}
	})

	t.Run("a bad phase or id is refused", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"job_ids": []int64{es.ID}, "phase": "predict"},
			{"job_ids": []int64{0}, "phase": "tm"},
			{"job_ids": []int64{}, "phase": "tm"},
		} {
			if code, _ := post(t, body); code != http.StatusBadRequest {
				t.Errorf("%v: status %d, want 400", body, code)
			}
		}
	})
}

// resetJobs puts queued jobs back to "created", as the worker would after a run.
func resetJobs(t *testing.T, q db.Querier, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if err := q.UpdateJobStatus(testutil.Ctx(), db.UpdateJobStatusParams{ID: id, Status: "created", CurrentStep: ""}); err != nil {
			t.Fatalf("reset job %d: %v", id, err)
		}
	}
}

// sameTMMT reports whether phases run exactly TM and/or MT on the whole job,
// as the bulk bar's (manual) run or as automation.
func sameTMMT(p pipeline.TranslatePhases, tm, mt, manual bool) bool {
	return p.UseTM == tm && p.UseMT == mt && p.Manual == manual && p.TranslationIDs == nil
}
