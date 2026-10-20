package qa_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegration_QAHandlers_CrossTenantReturns404 pins that
// POST /api/jobs/{jobID}/run-qa and GET /api/jobs/{jobID}/qa-report return 404
// for another tenant's job. ListQAResultsForJob / CountQAIssuesByJob filter
// only by file_id + target_language, so the GetJobTenant guard is the whole
// tenant boundary.
//
// Anti-tautology: removing the GetJobTenant guard in either handler makes the
// corresponding subtest fail — HandleRunQA returns 500 (via the GetProject
// failure), HandleQAReport returns 200 + another tenant's data.
func TestIntegration_QAHandlers_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	// Tenant A — owns the project + file + job. No QA profile is needed for
	// this test: the tenant check fires before the handler reaches the QA
	// run path.
	userA, tenantA := testutil.RegisterUser(t, db.New(pool), "qax-a")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "QAX Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	fileA := testutil.SeedFile(t, q, projResp.Data.ID, "qax.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "es")

	// Tenant B — attacker. Registered separately; no shared resources.
	userB, tenantB := testutil.RegisterUser(t, db.New(pool), "qax-b")

	t.Run("RunQA", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/jobs/%d/run-qa", jobA.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("QAReport", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/qa-report", jobA.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// Sanity: tenant A's owner CAN read the QA report (200, even with no
	// results) — this distinguishes "tenant boundary works" from "endpoint
	// always returns 404 for everyone."
	t.Run("OwnerCanRead", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/jobs/%d/qa-report", jobA.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
	})

	_ = ctx // keep ctx in scope in case future subtests need DB readback
}

// TestIntegration_RunTranslationQA_PersistsAndScoped pins the on-save
// per-segment QA endpoint: POST /api/translations/{id}/qa
// recomputes QA against the stored target and PERSISTS it, so a follow-up GET
// returns the fresh results — this is what lets the CAT editor refresh badges
// after autosave without a manual full-job run.
//
// It also pins the tenant boundary: a cross-tenant POST returns 404 (the
// service resolves translation → project scoped to the caller's tenant).
//
// Anti-tautology: the number_added finding only fires because CheckNumbers is
// bidirectional (42 appears in the target but not the source) and emits a
// non-blocking warning for target-only numbers. Making it source-only makes the
// persist assertion fail (zero issues persisted).
func TestIntegration_RunTranslationQA_PersistsAndScoped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	userA, tenantA := testutil.RegisterUser(t, db.New(pool), "qaruna")

	// Project owned by tenant A.
	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name":             "QARun Project",
		"source_language":  "en",
		"target_languages": []string{"es"},
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	// QA profile with a single number_consistency rule (warning so it never
	// blocks), assigned to the project's settings profile.
	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name": "QARun QA", "description": "adr-063 fixture",
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var profResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &profResp)
	profileID := profResp.Data.ID

	rec = testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/qa-profiles/%d/rules", profileID),
		map[string]any{"check_type": "number_consistency", "enabled": true, "severity": "warning"},
		userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)

	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profileID}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Segment source has NO numbers; the stored target introduces "42".
	file := testutil.SeedFile(t, q, projectID, "qarun.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, q, file.ID, "There are items", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
	target := "Hay 42 cosas"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: tr.ID, HumanText: &target,
	}); err != nil {
		t.Fatalf("seed target text: %v", err)
	}

	// Cross-tenant attacker cannot run QA on tenant A's translation.
	userB, tenantB := testutil.RegisterUser(t, db.New(pool), "qarunb")
	t.Run("CrossTenant404", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	})

	// Reading is scoped too. RequireLinguistTranslationAccess passes every
	// non-linguist straight through, so the owner of another tenant reaches the
	// handler; only the handler's own tenant lookup stops it. QA messages carry
	// segment fragments (flagged terms, numbers, tags) and the display name of
	// whoever ignored a finding.
	t.Run("CrossTenantGET404", func(t *testing.T) {
		// Seed a finding to read, so a leak would return content rather than [].
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)

		rec = testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userB, tenantB)
		testutil.AssertStatus(t, rec, http.StatusNotFound)

		var leaked struct {
			Data []qaRow `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &leaked)
		if len(leaked.Data) > 0 {
			t.Fatalf("cross-tenant GET returned %d QA finding(s): %+v", len(leaked.Data), leaked.Data)
		}
	})

	// Owner runs QA → persisted results come back, including the target-only
	// number flagged by the bidirectional CheckNumbers.
	t.Run("OwnerRunPersists", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, "POST",
			fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)

		var runResp struct {
			Data []qaRow `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &runResp)
		if !hasCheck(runResp.Data, "number_added") {
			t.Fatalf("expected number_added issue from POST, got %+v", runResp.Data)
		}

		// The result was PERSISTED — a follow-up GET returns it too.
		rec = testutil.DoRequest(t, router, "GET",
			fmt.Sprintf("/api/translations/%d/qa", tr.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var getResp struct {
			Data []qaRow `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &getResp)
		if !hasCheck(getResp.Data, "number_added") {
			t.Fatalf("POST did not persist — GET returned %+v", getResp.Data)
		}
	})
}

type qaRow struct {
	CheckType string `json:"check_type"`
	Severity  string `json:"severity"`
}

// hasCheck reports whether any row has the given check_type — the specific
// type matters (a generic "len > 0" would pass on an unrelated issue).
func hasCheck(rows []qaRow, checkType string) bool {
	for _, r := range rows {
		if r.CheckType == checkType {
			return true
		}
	}
	return false
}

// TestIntegration_RunQAJob_PrunesFixedSegments pins what a job-wide run must do
// to segments whose findings have changed since the last run.
//
// A run skips the write for a segment that has no findings now and had none
// before (one transaction + one prune DELETE per segment is
// most of a large file's run cost, and for a clean segment both are no-ops).
// "Had none before" is the load-bearing half of that condition: widen the skip
// to "no findings now" and a fixed segment keeps its stale red dot forever.
//
// Anti-tautology: dropping `&& !hadFindings[...]` from the skip in
// Service.RunAndPersistJob makes StaleFindingIsPruned fail with the old finding
// still in the report.
func TestIntegration_RunQAJob_PrunesFixedSegments(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	userA, tenantA := testutil.RegisterUser(t, q, "qaprune")

	rec := testutil.DoRequest(t, router, "POST", "/api/projects", map[string]any{
		"name": "QA Prune Project", "source_language": "en", "target_languages": []string{"es"},
	}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var projResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &projResp)
	projectID := projResp.Data.ID

	rec = testutil.DoRequest(t, router, "POST", "/api/qa-profiles", map[string]string{
		"name": "QA Prune profile", "description": "prune guard",
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
		map[string]any{"check_type": "number_consistency", "enabled": true, "severity": "error"},
		userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)
	rec = testutil.DoRequest(t, router, "PATCH",
		fmt.Sprintf("/api/projects/%d/settings", projectID),
		map[string]any{"qa_profile_id": profResp.Data.ID}, userA, tenantA)
	testutil.AssertStatus(t, rec, http.StatusOK)

	file := testutil.SeedFile(t, q, projectID, "qaprune.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	// Segment 1 drops the number → one finding. Segment 2 is correct throughout,
	// so it is the "clean segment" the skip is meant to save work on.
	seg1 := testutil.SeedSegment(t, q, file.ID, "There are 42 items", 1)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusNeedsReview)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Nothing numeric here", 2)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusNeedsReview)

	setTarget := func(t *testing.T, id int64, text string) {
		t.Helper()
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: id, HumanText: &text}); err != nil {
			t.Fatalf("set target text: %v", err)
		}
	}
	setTarget(t, tr1.ID, "Hay cosas")
	setTarget(t, tr2.ID, "Nada numerico aqui")

	runJob := func(t *testing.T) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "POST", fmt.Sprintf("/api/jobs/%d/run-qa", job.ID), nil, userA, tenantA)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}
	findingsFor := func(t *testing.T, translationID int64) []db.ListQAResultsForTranslationRow {
		t.Helper()
		rows, err := q.ListQAResultsForTranslation(ctx, translationID)
		if err != nil {
			t.Fatalf("list QA results: %v", err)
		}
		return rows
	}

	runJob(t)
	if got := findingsFor(t, tr1.ID); len(got) != 1 {
		t.Fatalf("precondition: segment 1 should have one finding, got %+v", got)
	}
	if got := findingsFor(t, tr2.ID); len(got) != 0 {
		t.Fatalf("precondition: segment 2 should be clean, got %+v", got)
	}

	t.Run("StaleFindingIsPruned", func(t *testing.T) {
		setTarget(t, tr1.ID, "Hay 42 cosas")
		runJob(t)
		if got := findingsFor(t, tr1.ID); len(got) != 0 {
			t.Fatalf("a fixed segment must lose its finding, got %+v", got)
		}
	})

	t.Run("CleanSegmentStaysClean", func(t *testing.T) {
		runJob(t)
		if got := findingsFor(t, tr2.ID); len(got) != 0 {
			t.Fatalf("segment 2 never had a finding and must still have none, got %+v", got)
		}
	})

	t.Run("FindingComesBack", func(t *testing.T) {
		setTarget(t, tr1.ID, "Hay cosas")
		runJob(t)
		if got := findingsFor(t, tr1.ID); len(got) != 1 {
			t.Fatalf("the finding must return once the number is dropped again, got %+v", got)
		}
	})
}

// UpdateQAProfile is scoped by tenant in the query itself, not only by the
// lookup callers run before it.
func TestIntegration_UpdateQAProfile_OtherTenantMatchesNothing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantA := testutil.RegisterUser(t, db.New(pool), "qaup-a")
	_, tenantB := testutil.RegisterUser(t, db.New(pool), "qaup-b")
	profile, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: tenantA, Name: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateQAProfile(ctx, db.UpdateQAProfileParams{ID: profile.ID, TenantID: tenantB, Name: "Hijacked"}); err == nil {
		t.Fatal("an update under another tenant matched the profile")
	}
	got, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: profile.ID, TenantID: tenantA})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Strict" {
		t.Errorf("name = %q, want it unchanged", got.Name)
	}
	if _, err := q.UpdateQAProfile(ctx, db.UpdateQAProfileParams{ID: profile.ID, TenantID: tenantA, Name: "Lenient"}); err != nil {
		t.Errorf("the owning tenant's update failed: %v", err)
	}
}
