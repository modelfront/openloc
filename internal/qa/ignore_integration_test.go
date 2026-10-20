package qa_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// finding is the wire shape both per-translation QA endpoints return
// (qa.ResultDTO), narrowed to what these tests assert on.
type finding struct {
	CheckType     string  `json:"check_type"`
	Severity      string  `json:"severity"`
	Message       string  `json:"message"`
	Ignored       bool    `json:"ignored"`
	IgnoredByName *string `json:"ignored_by_name"`
}

type report struct {
	Summary struct {
		Error   int `json:"error"`
		Warning int `json:"warning"`
		Info    int `json:"info"`
		Ignored int `json:"ignored"`
	} `json:"summary"`
	Results []finding `json:"results"`
}

func findCheck(rows []finding, checkType string) *finding {
	for i := range rows {
		if rows[i].CheckType == checkType {
			return &rows[i]
		}
	}
	return nil
}

// The check under test. number_consistency fires at the rule's severity when
// a SOURCE number is missing from the target (a target-only number is the
// softer number_added, always a warning — not what we want here).
const check = "number_consistency"

// TestIntegration_QAIgnore pins the per-finding ignore
// end to end over HTTP: the flag lands on the row and in the report, survives
// BOTH write paths (per-segment run and job-wide run), unblocks confirm, and
// lapses when the finding changes or stops firing.
//
// Anti-tautology: reverting UpsertQAResult to the old delete-then-insert makes
// FlagSurvivesPerSegmentRun and FlagSurvivesJobRun fail; removing the
// `!issue.Ignored` guard in CheckResult.HasErrors makes
// IgnoredErrorDoesNotBlockConfirm fail with 422.
func TestIntegration_QAIgnore(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	userA, tenantA := testutil.RegisterUser(t, q, "qaign")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "QA Ignore Project", "source_language": "en", "target_languages": []string{"es"},
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// number_consistency at severity=error so the finding blocks confirm.
	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name": "QA Ignore profile", "description": "adr-20260907-1104 fixture",
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var profResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profResp)
	rec = testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/qa-profiles/%d/rules", profResp.Data.ID),
		map[string]any{"check_type": check, "enabled": true, "severity": "error"},
		userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profResp.Data.ID}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Source carries two numbers. A target that drops "42" fires
	// number_consistency (error); dropping "7" instead is a different message.
	const missing42 = "Hay cosas en 7 cajas"
	const missing7 = "Hay 42 cosas en cajas"
	const fixed = "Hay 42 cosas en 7 cajas"
	file := testutil.SeedFile(t, q, projectID, "qaign.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "There are 42 items in 7 boxes", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	job := testutil.SeedJob(t, q, file.ID, "es")
	setTarget := func(t *testing.T, text string) {
		t.Helper()
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &text}); err != nil {
			t.Fatalf("set target text: %v", err)
		}
	}
	setTarget(t, missing42)

	runSegment := func(t *testing.T) []finding {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []finding `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data
	}
	getReport := func(t *testing.T) report {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", fmt.Sprintf("/api/jobs/%d/qa-report", job.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data report `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		return resp.Data
	}
	ignorePath := func(checkType string) string {
		return fmt.Sprintf("/api/translations/%d/qa/%s/ignore", tr.ID, checkType)
	}
	confirm := func(t *testing.T, text string) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequest(t, router, "PUT",
			fmt.Sprintf("/api/files/%d/translations/%d", file.ID, tr.ID),
			map[string]any{"human_text": text, "editing_time_ms": 0}, userA, tenantA)
	}

	// Baseline: the finding exists, is active, and is an error.
	rows := runSegment(t)
	if f := findCheck(rows, check); f == nil || f.Ignored || f.Severity != "error" {
		t.Fatalf("expected an active %s error, got %+v", check, rows)
	}

	t.Run("ConfirmBlockedBeforeIgnore", func(t *testing.T) {
		testutil.AssertStatus(t, confirm(t, missing42), http.StatusUnprocessableEntity)
	})

	t.Run("IgnoreFlagsRowAndReport", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", ignorePath(check),
			nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []finding `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		f := findCheck(resp.Data, check)
		if f == nil || !f.Ignored {
			t.Fatalf("finding not flagged ignored: %+v", resp.Data)
		}
		if f.IgnoredByName == nil || *f.IgnoredByName != "qaign" {
			t.Errorf("ignored_by_name should be the caller's name, got %+v", f.IgnoredByName)
		}

		rep := getReport(t)
		if rep.Summary.Error != 0 || rep.Summary.Ignored != 1 {
			t.Errorf("report counts: want error=0 ignored=1, got %+v", rep.Summary)
		}
		if len(rep.Results) != 1 || !rep.Results[0].Ignored {
			t.Errorf("report must still list the ignored finding, flagged: %+v", rep.Results)
		}
	})

	t.Run("UnignoreClearsEverything", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "DELETE", ignorePath(check), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []finding `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		f := findCheck(resp.Data, check)
		if f == nil || f.Ignored || f.IgnoredByName != nil {
			t.Fatalf("un-ignore must clear flag and author: %+v", f)
		}
		rep := getReport(t)
		if rep.Summary.Error != 1 || rep.Summary.Ignored != 0 {
			t.Errorf("report counts after un-ignore: want error=1 ignored=0, got %+v", rep.Summary)
		}
		// Re-ignore for the subtests below.
		rec = testutil.DoRequest(t, router, "POST", ignorePath(check), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	t.Run("FlagSurvivesPerSegmentRun", func(t *testing.T) {
		rows := runSegment(t)
		if f := findCheck(rows, check); f == nil || !f.Ignored {
			t.Fatalf("per-segment run dropped the ignore: %+v", rows)
		}
	})

	t.Run("FlagSurvivesJobRun", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		rep := getReport(t)
		if rep.Summary.Error != 0 || rep.Summary.Ignored != 1 {
			t.Fatalf("job-wide run dropped the ignore: %+v", rep.Summary)
		}
	})

	t.Run("IgnoredErrorDoesNotBlockConfirm", func(t *testing.T) {
		testutil.AssertStatus(t, confirm(t, missing42), http.StatusOK)
		// The editor re-runs per-segment QA right after a confirm; the ignore
		// must still be there (same text → same finding).
		rows := runSegment(t)
		if f := findCheck(rows, check); f == nil || !f.Ignored {
			t.Fatalf("per-segment run after confirm dropped the ignore: %+v", rows)
		}
		rep := getReport(t)
		if rep.Summary.Error != 0 || rep.Summary.Ignored != 1 {
			t.Fatalf("report after confirm: %+v", rep.Summary)
		}
	})

	t.Run("ChangedFindingIsNew", func(t *testing.T) {
		setTarget(t, missing7)
		rows := runSegment(t)
		f := findCheck(rows, check)
		if f == nil {
			t.Fatalf("expected %s for the missing 7, got %+v", check, rows)
		}
		if f.Ignored {
			t.Errorf("a different missing number is a different finding; the ignore must not carry over: %+v", f)
		}
		if !strings.Contains(f.Message, "7") || strings.Contains(f.Message, "42") {
			t.Errorf("message should name only the new missing number: %q", f.Message)
		}
	})

	t.Run("PrunedWhenFixedAndFreshWhenBack", func(t *testing.T) {
		setTarget(t, fixed)
		if rows := runSegment(t); len(rows) != 0 {
			t.Fatalf("fixed segment must have no findings, got %+v", rows)
		}
		// No row → nothing to ignore; and when the finding returns it is red.
		rec := testutil.DoRequest(t, router, "POST", ignorePath(check), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		setTarget(t, missing42)
		rows := runSegment(t)
		if f := findCheck(rows, check); f == nil || f.Ignored {
			t.Fatalf("returning finding must be active again: %+v", rows)
		}
	})

	t.Run("NoSuchFinding404", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", ignorePath("punctuation"), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	userB, tenantB := testutil.RegisterUser(t, q, "qaignb")
	t.Run("CrossTenant404", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST", ignorePath(check), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		rec = testutil.DoRequest(t, router, "DELETE", ignorePath(check), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		// And nothing changed for tenant A.
		rows := runSegment(t)
		if f := findCheck(rows, check); f == nil || f.Ignored {
			t.Fatalf("cross-tenant call altered tenant A's finding: %+v", rows)
		}
	})

	// Access scope is "linguists when assigned": pin both halves, so neither an
	// under-restrictive nor an over-restrictive gate can pass unnoticed.
	ling := testutil.SeedUserWithRole(t, q, tenantA, "qaign-ling@test.com", "pass", "linguist")
	// Both verbs, because each route line carries its own middleware: dropping
	// it from one leaves the other's test green while an unassigned linguist
	// walks through, and the query behind them is tenant-scoped but not
	// assignment-scoped, so nothing downstream would stop them.
	t.Run("UnassignedLinguist403", func(t *testing.T) {
		for _, method := range []string{"POST", "DELETE"} {
			rec := testutil.DoRequestWithRole(t, router, method, ignorePath(check), nil, ling.ID, tenantA, "linguist")
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s as unassigned linguist: want 403, got %d (body: %s)", method, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("AssignedLinguistCanIgnore", func(t *testing.T) {
		project, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantA})
		if err != nil {
			t.Fatalf("get project: %v", err)
		}
		testutil.SeedJobStep(t, q, project.SettingsProfileID, job.ID, &ling.ID)
		rec := testutil.DoRequestWithRole(t, router, "POST", ignorePath(check), nil, ling.ID, tenantA, "linguist")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []finding `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		f := findCheck(resp.Data, check)
		if f == nil || !f.Ignored || f.IgnoredByName == nil || *f.IgnoredByName != ling.Name {
			t.Fatalf("assigned linguist's ignore not recorded under their name: %+v", f)
		}
		rec = testutil.DoRequestWithRole(t, router, "DELETE", ignorePath(check), nil, ling.ID, tenantA, "linguist")
		testutil.AssertStatus(t, rec, http.StatusOK)
	})
}
