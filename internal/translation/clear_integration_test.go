package translation_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type clearCtx struct {
	router   http.Handler
	q        db.Querier
	pool     *pgxpool.Pool
	userID   int64
	tenantID int64
	project  int64
	file     int64
	job      db.Job
	seg      [4]db.Segment // positions 1..4
	tr       [4]db.Translation
}

func seedClearContext(t *testing.T, prefix string) *clearCtx {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, prefix)
	projectID := createProject(t, router, prefix+" Project", userID, tenantID)
	file := testutil.SeedFile(t, q, projectID, prefix+".txt", db.FileFormatTxt)
	c := &clearCtx{
		router:   router,
		q:        q,
		pool:     pool,
		userID:   userID,
		tenantID: tenantID,
		project:  projectID,
		file:     file.ID,
	}
	for i := 0; i < 4; i++ {
		c.seg[i] = testutil.SeedSegment(t, q, file.ID, fmt.Sprintf("hello %d", i+1), int32(i+1))
		c.tr[i] = testutil.SeedTranslation(t, q, c.seg[i].ID, "es", db.TranslationStatusNeedsReview)
	}
	c.job = testutil.SeedJob(t, q, file.ID, "es")
	return c
}

func (c *clearCtx) setTranslationFields(t *testing.T, id int64, status db.TranslationStatus, fields map[string]any) {
	t.Helper()
	args := []any{string(status), id}
	setClauses := "status = $1::translation_status"
	i := 3
	for k, v := range fields {
		setClauses += fmt.Sprintf(", %s = $%d", k, i)
		args = append(args, v)
		i++
	}
	q := fmt.Sprintf("UPDATE translations SET %s WHERE id = $2", setClauses)
	if _, err := c.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("setTranslationFields(%d): %v", id, err)
	}
}

func (c *clearCtx) reload(t *testing.T, id int64) db.Translation {
	t.Helper()
	tr, err := c.q.GetTranslation(context.Background(), id)
	if err != nil {
		t.Fatalf("reload(%d): %v", id, err)
	}
	return tr
}

// assertActiveText fails unless the row still shows want. It is how a test
// proves a rejected clear wrote nothing: status alone can't, because clear
// writes needs_review and the rows are seeded needs_review.
func (c *clearCtx) assertActiveText(t *testing.T, id int64, want, why string) {
	t.Helper()
	r := c.reload(t, id)
	if r.ActiveText == nil {
		t.Errorf("%s: active_text is NULL, want %q", why, want)
	} else if *r.ActiveText != want {
		t.Errorf("%s: active_text=%q, want %q", why, *r.ActiveText, want)
	}
}

func assertAllDerivedFieldsNull(t *testing.T, tr db.Translation) {
	t.Helper()
	if tr.Status != db.TranslationStatusNeedsReview {
		t.Errorf("status: want needs_review, got %q", tr.Status)
	}
	// Every derived field resets to NULL/0 on clear. active_text is the
	// canonical visible target.
	if tr.ActiveText != nil {
		t.Errorf("active_text: want NULL, got %q", *tr.ActiveText)
	}
	if tr.ActiveRichText != nil {
		t.Errorf("active_rich_text: want NULL, got %q", *tr.ActiveRichText)
	}
	// origin, tm_match_percent and predict_outcome are nullable and reset
	// to NULL on clear.
	if tr.TmMatchPercent != nil {
		t.Errorf("tm_match_percent: want NULL, got %v", *tr.TmMatchPercent)
	}
	if tr.Origin.Valid {
		t.Errorf("origin: want NULL, got %q", tr.Origin.TranslationOrigin)
	}
	if tr.PredictOutcome.Valid {
		t.Errorf("predict_outcome: want NULL, got %q", tr.PredictOutcome.PredictOutcome)
	}
	// Stale "user history" — productivity metrics must reset so the
	// post-clear segment isn't carrying old time / edit distance.
	if tr.EditingTimeMs != 0 {
		t.Errorf("editing_time_ms: want 0, got %d", tr.EditingTimeMs)
	}
	if tr.EditDistance != 0 {
		t.Errorf("edit_distance: want 0, got %d", tr.EditDistance)
	}
	if tr.EditedBy != nil {
		t.Errorf("edited_by: want NULL, got %d", *tr.EditedBy)
	}
}

// per_segment Clear of an already-untranslated row is a no-op on
// content (idempotent) but MUST still bump updated_at so a concurrent
// edit that captured the prior baseline conflicts on save.
func TestIntegrationClear_PerSegment_IdempotentOnUntranslated(t *testing.T) {
	c := seedClearContext(t, "clear-seg-idem")
	before := c.reload(t, c.tr[0].ID)
	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	after := c.reload(t, c.tr[0].ID)
	if after.Status != db.TranslationStatusNeedsReview {
		t.Errorf("status: want needs_review, got %q", after.Status)
	}
	if !after.UpdatedAt.Time.After(before.UpdatedAt.Time) {
		t.Errorf("updated_at must advance even for idempotent clear (concurrent-edit conflict guard); before=%v after=%v",
			before.UpdatedAt.Time, after.UpdatedAt.Time)
	}
}

// Defensive: clearing must not leak across jobs that share a file
// (file has en→es AND en→fr jobs, only es should be cleared by the
// per-job clear).
func TestIntegrationClear_PerJob_DoesNotAffectOtherLanguagesOnSameFile(t *testing.T) {
	c := seedClearContext(t, "clear-job-lang-iso")
	// Add a French job on the SAME file. Set up a French translation
	// with MT content, so we can verify it survives.
	frJob := testutil.SeedJob(t, c.q, c.file, "fr")
	_ = frJob
	frTr := testutil.SeedTranslation(t, c.q, c.seg[0].ID, "fr", db.TranslationStatusNeedsReview)
	frMT := "Bonjour"
	c.setTranslationFields(t, frTr.ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &frMT,
	})
	// es content to be cleared
	esMT := "Hola"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &esMT,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[0].ID))
	frAfter := c.reload(t, frTr.ID)
	if frAfter.Status != db.TranslationStatusNeedsReview {
		t.Errorf("fr row in same file must NOT be cleared by es-targeted job clear; status=%q", frAfter.Status)
	}
	if frAfter.ActiveText == nil || *frAfter.ActiveText != frMT {
		t.Errorf("fr row's active_text was modified; got %v", frAfter.ActiveText)
	}
}

func TestIntegrationClear_PerSegment_NullsAllDerivedFields(t *testing.T) {
	c := seedClearContext(t, "clear-seg-null")
	mt := "Hola mundo"
	percent := int16(85)
	editorID := c.userID
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text":      &mt,
		"origin":           string(db.TranslationOriginTm),
		"tm_match_percent": &percent,
		"predict_outcome":  string(db.PredictOutcomeApproved),
		// "User history" fields: ensure Clear wipes these alongside content.
		"editing_time_ms": int64(15000),
		"edit_distance":   int32(7),
		"edited_by":       &editorID,
	})
	before := c.reload(t, c.tr[0].ID)

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	after := c.reload(t, c.tr[0].ID)
	assertAllDerivedFieldsNull(t, after)
	if !after.UpdatedAt.Time.After(before.UpdatedAt.Time) {
		t.Errorf("updated_at: want advanced past %v, got %v", before.UpdatedAt.Time, after.UpdatedAt.Time)
	}
}

func TestIntegrationClear_PerSegment_LockedReturns409(t *testing.T) {
	c := seedClearContext(t, "clear-seg-locked")
	mt := "Hola"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
		"locked":      true,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Row must be untouched — neither status nor active_text changed.
	after := c.reload(t, c.tr[0].ID)
	if after.Status != db.TranslationStatusNeedsReview {
		t.Errorf("status: locked-clear must not change it, got %q", after.Status)
	}
	if after.ActiveText == nil || *after.ActiveText != mt {
		t.Errorf("active_text: locked-clear must not null it, got %v", after.ActiveText)
	}
}

func TestIntegrationClear_PerSegment_CrossTenant404(t *testing.T) {
	c := seedClearContext(t, "clear-seg-xtenant-a")
	mt := "Hola"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	// Second tenant's user, hitting tenant A's path.
	otherUser, otherTenant := registerUser(t, c.q, "clear-seg-xtenant-b")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, otherUser, otherTenant)
	if rec.Code == http.StatusOK {
		t.Fatalf("cross-tenant clear must NOT succeed; got 200 (body: %s)", rec.Body.String())
	}
	after := c.reload(t, c.tr[0].ID)
	if after.ActiveText == nil || *after.ActiveText != mt {
		t.Errorf("cross-tenant clear must not modify the row; active_text=%v", after.ActiveText)
	}
}

// per_segment AuthZ — handler reads fileID from URL but SQL clears by
// translationID. If a tenant member routes a translation's clear
// through a different (same-tenant, same-lang) file's URL, the
// workflow-step gate is checked against the WRONG file's job. A
// step-1-locked translation could be cleared via a step-0 file's URL.
func TestIntegrationClear_PerSegment_CrossFileMismatch_Rejected(t *testing.T) {
	c := seedClearContext(t, "clear-seg-xfile")
	// Second file under the SAME project — passes RequireFileOwnership
	// middleware. This is the attacker's URL.
	file2 := testutil.SeedFile(t, c.q, c.project, "f2.txt", db.FileFormatTxt)
	testutil.SeedJob(t, c.q, file2.ID, "es")
	// File 1's translation that we'll try to clear via file 2's URL.
	mt := "Hola"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", file2.ID, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	if rec.Code == http.StatusOK {
		t.Fatalf("cross-file clear must NOT succeed; got 200 (body: %s)", rec.Body.String())
	}
	if r := c.reload(t, c.tr[0].ID); r.Status != db.TranslationStatusNeedsReview || r.ActiveText == nil || *r.ActiveText != mt {
		t.Errorf("translation modified by cross-file clear; status=%q active_text=%v", r.Status, r.ActiveText)
	}
}

func TestIntegrationClear_PerJob_NonConfirmedPreservesApproved(t *testing.T) {
	c := seedClearContext(t, "clear-job-nc")
	approved, auto, mt := "Approved text", "Auto-approved", "MT text"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusApproved, map[string]any{
		"active_text": &approved,
	})
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusAutoApproved, map[string]any{
		"active_text": &auto,
	})
	c.setTranslationFields(t, c.tr[2].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	c.setTranslationFields(t, c.tr[3].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "non_confirmed"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if r := c.reload(t, c.tr[0].ID); r.Status != db.TranslationStatusApproved {
		t.Errorf("approved row was cleared (status=%q) — non_confirmed scope must preserve it", r.Status)
	}
	if r := c.reload(t, c.tr[1].ID); r.Status != db.TranslationStatusAutoApproved {
		t.Errorf("auto_approved row was cleared (status=%q) — non_confirmed scope must preserve it", r.Status)
	}
	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[2].ID))
	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[3].ID))
}

func TestIntegrationClear_PerJob_AllScope(t *testing.T) {
	c := seedClearContext(t, "clear-job-all")
	approved := "Approved"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusApproved, map[string]any{
		"active_text": &approved,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	if r := c.reload(t, c.tr[0].ID); r.Status != db.TranslationStatusNeedsReview {
		t.Errorf("scope=all must clear approved rows too; got %q", r.Status)
	}
}

func TestIntegrationClear_PerJob_LockedRowsImmune(t *testing.T) {
	c := seedClearContext(t, "clear-job-locked")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt, "locked": true,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	r := c.reload(t, c.tr[0].ID)
	if r.Status != db.TranslationStatusNeedsReview {
		t.Errorf("locked row was cleared in scope=all; status=%q", r.Status)
	}
	if r.ActiveText == nil || *r.ActiveText != mt {
		t.Errorf("locked row's active_text changed; got %v", r.ActiveText)
	}
}

func TestIntegrationClear_PerJob_PipelineRunning409(t *testing.T) {
	// Both pipeline-running statuses ("queued" + "in_progress") must
	// block Clear. Threshold-coverage rule (see step_order boundary
	// tests): assert both sides of the check, not just one.
	for _, status := range []string{"queued", "in_progress"} {
		t.Run("status="+status, func(t *testing.T) {
			c := seedClearContext(t, "clear-job-running-"+status)
			if err := c.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{
				ID: c.job.ID, Status: status, CurrentStep: "MT",
			}); err != nil {
				t.Fatalf("set job status: %v", err)
			}

			rec := testutil.DoRequest(t, c.router, "POST",
				fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
				map[string]string{"scope": "all"},
				c.userID, c.tenantID)
			testutil.AssertStatus(t, rec, http.StatusConflict)
		})
	}
}

// Anti-tautology: change parseClearScope's default branch to ok=true → fails.
func TestIntegrationClear_PerJob_BadScope400(t *testing.T) {
	c := seedClearContext(t, "clear-job-bad")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "everything"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	c.assertActiveText(t, c.tr[0].ID, mt, "bad scope must not clear")
}

func TestIntegrationClear_PerJob_UnassignedLinguist403(t *testing.T) {
	c := seedClearContext(t, "clear-job-role")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	// Clear is allowed for managers + the assigned linguist; a linguist
	// NOT assigned to the job is rejected.
	stranger := testutil.SeedUserWithRole(t, c.q, c.tenantID, "clear-stranger@test.com", "pass", "linguist")
	rec := testutil.DoRequestWithRole(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"},
		stranger.ID, c.tenantID, "linguist")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unassigned linguist clear must be 403; got %d (body: %s)", rec.Code, rec.Body.String())
	}
	c.assertActiveText(t, c.tr[0].ID, mt, "role-rejected clear must not modify the row")
}

// parkJobOnStep inserts a workflow_step at the given step_order and sets
// the job's current_step_id to a fresh assignment of it. Used by the
// step-boundary tests below.
func (c *clearCtx) parkJobOnStep(t *testing.T, stepOrder int) {
	t.Helper()
	ctx := context.Background()
	project, err := c.q.GetProject(ctx, db.GetProjectParams{ID: c.project, TenantID: c.tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	var stepID int64
	if err := c.pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required)
		 VALUES ($1, $2, $3, 'member') RETURNING id`,
		project.SettingsProfileID, fmt.Sprintf("Step-%d", stepOrder), stepOrder).Scan(&stepID); err != nil {
		t.Fatalf("insert workflow_step: %v", err)
	}
	var jsaID int64
	if err := c.pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, status)
		 VALUES ($1, $2, 'in_progress') RETURNING id`,
		c.job.ID, stepID).Scan(&jsaID); err != nil {
		t.Fatalf("insert job_step_assignment: %v", err)
	}
	if _, err := c.pool.Exec(ctx,
		`UPDATE jobs SET current_step_id = $1 WHERE id = $2`, jsaID, c.job.ID); err != nil {
		t.Fatalf("set current_step_id: %v", err)
	}
}

// Positive case: job on step_order=0 (the first step — workflow.HandleSetSteps
// assigns step_order via int32(i) loop index starting at 0). MUST 200.
//
// Anti-tautology: change workflow.JobInFirstStep to `step.StepOrder == 1`
// → this test goes RED with 403.
func TestIntegrationClear_PerJob_InFirstStep_Returns200(t *testing.T) {
	c := seedClearContext(t, "clear-job-step0")
	c.parkJobOnStep(t, 0)
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[0].ID))
}

// Strict-boundary negative case: job on step_order=1 (one past first).
// MUST 403. Pairs with the step_order=0 test above so the threshold is
// pinned exactly — a single-sided test at step_order=2 would be satisfied by BOTH `== 0` and `== 1` checks and can't distinguish.
func TestIntegrationClear_PerJob_StepOrder1_Returns403(t *testing.T) {
	c := seedClearContext(t, "clear-job-step1")
	c.parkJobOnStep(t, 1)
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	c.assertActiveText(t, c.tr[0].ID, mt, "step-gated clear must not modify the row")
}

// per_job_CrossTenant404 — pins the RequireJobOwnership middleware.
// Without it, a tenant B user knowing a tenant A job ID could clear
// tenant A's translations.
func TestIntegrationClear_PerJob_CrossTenant404(t *testing.T) {
	c := seedClearContext(t, "clear-job-xt-a")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	otherUser, otherTenant := registerUser(t, c.q, "clear-job-xt-b")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "all"}, otherUser, otherTenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	c.assertActiveText(t, c.tr[0].ID, mt, "cross-tenant per-job clear must not modify the row")
}

// per_project_EmptyProject — project with no jobs (no files yet).
// Should return 200 with cleared=0 (not crash, not 4xx).
func TestIntegrationClear_PerProject_EmptyProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	userID, tenantID := registerUser(t, q, "clear-proj-empty")
	projectID := createProject(t, router, "Empty Proj", userID, tenantID)
	_ = q

	rec := testutil.DoRequest(t, router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", projectID),
		map[string]string{"scope": "all"}, userID, tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// per_project_AnyJobPast0_Rejected — atomic semantics: if ANY job in
// the project is past step 0, the whole call must reject and nothing
// is cleared. Pre-flights are atomic.
func TestIntegrationClear_PerProject_AnyJobPastStep0_Rejected(t *testing.T) {
	c := seedClearContext(t, "clear-proj-atomic-step")
	// First file/job stays at step 0 (no workflow setup, defaults to in-first-step).
	// Add a second file + job, park its job on step_order=1.
	file2 := testutil.SeedFile(t, c.q, c.project, "f2.txt", db.FileFormatTxt)
	job2 := testutil.SeedJob(t, c.q, file2.ID, "es")
	ctx := context.Background()
	project, _ := c.q.GetProject(ctx, db.GetProjectParams{ID: c.project, TenantID: c.tenantID})
	var step1ID int64
	if err := c.pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required)
		 VALUES ($1, 'Review', 1, 'member') RETURNING id`,
		project.SettingsProfileID).Scan(&step1ID); err != nil {
		t.Fatalf("insert workflow_step: %v", err)
	}
	var jsaID int64
	if err := c.pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, status)
		 VALUES ($1, $2, 'in_progress') RETURNING id`,
		job2.ID, step1ID).Scan(&jsaID); err != nil {
		t.Fatalf("insert jsa: %v", err)
	}
	if _, err := c.pool.Exec(ctx,
		`UPDATE jobs SET current_step_id = $1 WHERE id = $2`, jsaID, job2.ID); err != nil {
		t.Fatalf("update job: %v", err)
	}

	// First file has data we'll check stays put.
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", c.project),
		map[string]string{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	// Atomic semantics: even file 1 (which is on step 0) must NOT be cleared.
	c.assertActiveText(t, c.tr[0].ID, mt, "file 1's row must survive while file 2 is past step 0")
}

// per_project_AnyJobRunning_Rejected — atomic semantics for the
// pipeline-status pre-flight. Same shape as the step boundary test.
func TestIntegrationClear_PerProject_AnyJobRunning_Rejected(t *testing.T) {
	c := seedClearContext(t, "clear-proj-atomic-running")
	file2 := testutil.SeedFile(t, c.q, c.project, "f2.txt", db.FileFormatTxt)
	job2 := testutil.SeedJob(t, c.q, file2.ID, "es")
	if err := c.q.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{
		ID: job2.ID, Status: "in_progress", CurrentStep: "MT",
	}); err != nil {
		t.Fatalf("set job 2 in_progress: %v", err)
	}

	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", c.project),
		map[string]string{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusConflict)
	c.assertActiveText(t, c.tr[0].ID, mt, "file 1's row must survive while job 2 is in flight")
}

func TestIntegrationClear_PerProject_HappyPath(t *testing.T) {
	c := seedClearContext(t, "clear-proj")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	// Second file under same project — Clear-by-project must sweep both.
	file2 := testutil.SeedFile(t, c.q, c.project, "f2.txt", db.FileFormatTxt)
	seg := testutil.SeedSegment(t, c.q, file2.ID, "hello other", 1)
	tr := testutil.SeedTranslation(t, c.q, seg.ID, "es", db.TranslationStatusNeedsReview)
	c.setTranslationFields(t, tr.ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	testutil.SeedJob(t, c.q, file2.ID, "es")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", c.project),
		map[string]string{"scope": "all"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[0].ID))
	assertAllDerivedFieldsNull(t, c.reload(t, tr.ID))
}

func TestIntegrationClear_PerProject_CrossTenant404(t *testing.T) {
	c := seedClearContext(t, "clear-proj-xt-a")
	mt := "MT"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	otherUser, otherTenant := registerUser(t, c.q, "clear-proj-xt-b")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", c.project),
		map[string]string{"scope": "all"},
		otherUser, otherTenant)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	c.assertActiveText(t, c.tr[0].ID, mt, "cross-tenant project clear must not modify the row")
}

// Load-bearing loop-close: if Clear leaves active_text non-null,
// runMTPhase's `active_text IS NULL` filter (ListUntranslatedByFile) skips
// the row → re-apply MT silently no-ops.
func TestIntegrationClear_ReApplyLoop_NonConfirmedThenApplyMT(t *testing.T) {
	c := seedClearContext(t, "clear-loop")
	ctx := context.Background()

	approved := "Hola aprobado"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusApproved, map[string]any{
		"active_text": &approved,
	})
	mtOriginal := "MT original"
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mtOriginal,
	})

	engine := testutil.SeedMTEngine(t, c.q, c.tenantID, "Mock2", "mock")
	project, err := c.q.GetProject(ctx, db.GetProjectParams{ID: c.project, TenantID: c.tenantID})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	testutil.SeedMTRouterForProfile(t, c.q, c.tenantID, project.SettingsProfileID, &engine.ID, nil)

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "non_confirmed"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	survived := c.reload(t, c.tr[0].ID)
	if survived.Status != db.TranslationStatusApproved {
		t.Fatalf("approved row must survive non_confirmed clear; status=%q", survived.Status)
	}
	if survived.ActiveText == nil || *survived.ActiveText != approved {
		t.Fatalf("approved row's active_text mutated by Clear; got %v", survived.ActiveText)
	}
	assertAllDerivedFieldsNull(t, c.reload(t, c.tr[1].ID))

	rec = testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/mt-apply", c.job.ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	reapplied := c.reload(t, c.tr[1].ID)
	if reapplied.Status != db.TranslationStatusNeedsReview {
		t.Fatalf("re-applied row must be needs_review; got %q", reapplied.Status)
	}
	if reapplied.ActiveText == nil || *reapplied.ActiveText == "" {
		t.Fatalf("re-applied row must have active_text; got %v", reapplied.ActiveText)
	}

	if r := c.reload(t, c.tr[0].ID); r.Status != db.TranslationStatusApproved {
		t.Fatalf("approved row mutated by re-apply MT; got %q", r.Status)
	}
}

// ---------------------------------------------------------------------------
// Clear drops the segment's stored QA findings
//
// qa_results.translation_id cascades on row DELETE, but Clear is an UPDATE that
// blanks active_text — the row survives, so nothing cascades. Every finding was
// computed against text that no longer exists, including its ignore state.
// ---------------------------------------------------------------------------

// seedQAFinding stores one finding on a translation, optionally already ignored.
func (c *clearCtx) seedQAFinding(t *testing.T, translationID int64, checkType, message string, ignored bool) {
	t.Helper()
	ctx := context.Background()
	if err := c.q.UpsertQAResult(ctx, db.UpsertQAResultParams{
		TranslationID: translationID,
		CheckType:     checkType,
		Severity:      "error",
		Message:       message,
	}); err != nil {
		t.Fatalf("seed qa_result(%d, %s): %v", translationID, checkType, err)
	}
	if !ignored {
		return
	}
	if _, err := c.q.SetQAResultIgnored(ctx, db.SetQAResultIgnoredParams{
		TranslationID: translationID,
		CheckType:     checkType,
		Ignored:       true,
		UserID:        c.userID,
		TenantID:      c.tenantID,
	}); err != nil {
		t.Fatalf("ignore qa_result(%d, %s): %v", translationID, checkType, err)
	}
}

func (c *clearCtx) qaCheckTypes(t *testing.T, translationID int64) []string {
	t.Helper()
	rows, err := c.q.ListQAResultsForTranslation(context.Background(), translationID)
	if err != nil {
		t.Fatalf("list qa_results(%d): %v", translationID, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CheckType)
	}
	return out
}

func (c *clearCtx) assertNoQAFindings(t *testing.T, translationID int64, label string) {
	t.Helper()
	if got := c.qaCheckTypes(t, translationID); len(got) > 0 {
		t.Errorf("%s: want no qa_results after clear, got %v", label, got)
	}
}

func TestIntegrationClear_PerSegment_DropsQAFindings(t *testing.T) {
	c := seedClearContext(t, "clear-seg-qa")
	mt := "Hola mundo"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	c.seedQAFinding(t, c.tr[0].ID, "terminology", "term 'world' not translated as 'mundo'", false)
	// An ignored finding must go too: the ignore said "this text is fine",
	// and that text is gone.
	c.seedQAFinding(t, c.tr[0].ID, "punctuation", "target ends with '.' but source does not", true)
	// A different segment is untouched by a per-segment clear.
	other := "Hola otro"
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &other,
	})
	c.seedQAFinding(t, c.tr[1].ID, "terminology", "unrelated finding", false)

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	c.assertNoQAFindings(t, c.tr[0].ID, "cleared segment")
	if got := c.qaCheckTypes(t, c.tr[1].ID); len(got) != 1 {
		t.Errorf("untouched segment: want its 1 finding kept, got %v", got)
	}
}

func TestIntegrationClear_PerJob_DropsQAFindingsOfClearedRowsOnly(t *testing.T) {
	c := seedClearContext(t, "clear-job-qa")
	mt := "Hola mundo"
	// tr[0] approved → survives a non_confirmed clear, so its findings must too.
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusApproved, map[string]any{
		"active_text": &mt,
	})
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	// tr[2] is locked → Clear skips it, so its findings stay.
	c.setTranslationFields(t, c.tr[2].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
		"locked":      true,
	})
	for _, id := range []int64{c.tr[0].ID, c.tr[1].ID, c.tr[2].ID} {
		c.seedQAFinding(t, id, "terminology", "term mismatch", false)
	}
	c.seedQAFinding(t, c.tr[1].ID, "punctuation", "trailing period", true)

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]string{"scope": "non_confirmed"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	c.assertNoQAFindings(t, c.tr[1].ID, "cleared row")
	if got := c.qaCheckTypes(t, c.tr[0].ID); len(got) != 1 {
		t.Errorf("approved row survives non_confirmed clear; its findings must too, got %v", got)
	}
	if got := c.qaCheckTypes(t, c.tr[2].ID); len(got) != 1 {
		t.Errorf("locked row is immune to clear; its findings must be too, got %v", got)
	}
}

func TestIntegrationClear_PerProject_DropsQAFindings(t *testing.T) {
	c := seedClearContext(t, "clear-proj-qa")
	mt := "Hola mundo"
	for i := 0; i < 2; i++ {
		c.setTranslationFields(t, c.tr[i].ID, db.TranslationStatusNeedsReview, map[string]any{
			"active_text": &mt,
		})
		c.seedQAFinding(t, c.tr[i].ID, "terminology", "term mismatch", i == 1)
	}

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/projects/%d/clear-translations", c.project),
		map[string]string{"scope": "all"},
		c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	c.assertNoQAFindings(t, c.tr[0].ID, "cleared row")
	c.assertNoQAFindings(t, c.tr[1].ID, "cleared row with an ignored finding")
}

// ---------------------------------------------------------------------------
// Clear drops the segment's AI comments, and keeps the human ones
//
// An AI comment is the QE+APE step's remark about a specific translation
// ("fixed the greeting"). Clear removes that translation, so the remark is
// describing text nobody can see — the same staleness qa_results had. A human
// comment is the opposite: someone's note about the SEGMENT, which outlives any
// one translation of it, and deleting it would destroy their work with no undo.
// ---------------------------------------------------------------------------

func (c *clearCtx) seedAIComment(t *testing.T, translationID int64, content string) {
	t.Helper()
	if _, err := c.q.CreateAIComment(context.Background(), db.CreateAICommentParams{
		TranslationID: translationID, Content: content,
	}); err != nil {
		t.Fatalf("seed AI comment(%d): %v", translationID, err)
	}
}

func (c *clearCtx) seedHumanComment(t *testing.T, translationID int64, content string) {
	t.Helper()
	if _, err := c.q.CreateComment(context.Background(), db.CreateCommentParams{
		TranslationID: translationID, UserID: &c.userID, Content: content,
	}); err != nil {
		t.Fatalf("seed human comment(%d): %v", translationID, err)
	}
}

func (c *clearCtx) commentContents(t *testing.T, translationID int64) []string {
	t.Helper()
	rows, err := c.q.ListCommentsByTranslationIDs(context.Background(), []int64{translationID})
	if err != nil {
		t.Fatalf("list comments(%d): %v", translationID, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Content)
	}
	return out
}

func (c *clearCtx) assertComments(t *testing.T, translationID int64, label string, want ...string) {
	t.Helper()
	got := c.commentContents(t, translationID)
	if len(got) != len(want) {
		t.Errorf("%s: comments = %v, want %v", label, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: comments = %v, want %v", label, got, want)
			return
		}
	}
}

func TestIntegrationClear_PerSegment_DropsAICommentsKeepsHuman(t *testing.T) {
	c := seedClearContext(t, "clear-seg-comments")
	mt := "Hola mundo"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	c.seedAIComment(t, c.tr[0].ID, "rewrote the greeting")
	c.seedHumanComment(t, c.tr[0].ID, "client prefers the formal register")

	// A different segment is untouched by a per-segment clear.
	other := "Hola otro"
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &other,
	})
	c.seedAIComment(t, c.tr[1].ID, "unrelated AI remark")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/files/%d/translations/%d/clear", c.file, c.tr[0].ID),
		nil, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	c.assertComments(t, c.tr[0].ID, "cleared segment", "client prefers the formal register")
	c.assertComments(t, c.tr[1].ID, "untouched segment", "unrelated AI remark")
}

func TestIntegrationClear_PerJob_DropsAICommentsKeepsHuman(t *testing.T) {
	c := seedClearContext(t, "clear-job-comments")
	for _, tr := range c.tr {
		mt := "Hola"
		c.setTranslationFields(t, tr.ID, db.TranslationStatusNeedsReview, map[string]any{
			"active_text": &mt,
		})
		c.seedAIComment(t, tr.ID, "AI remark")
		c.seedHumanComment(t, tr.ID, "human note")
	}

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]any{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	for _, tr := range c.tr {
		c.assertComments(t, tr.ID, fmt.Sprintf("translation %d", tr.ID), "human note")
	}
}

// A row the clear could not touch keeps its AI comment: the comment still
// describes the text that is still there.
func TestIntegrationClear_PerJob_LockedRowKeepsAIComment(t *testing.T) {
	c := seedClearContext(t, "clear-job-locked-comment")
	mt := "Hola"
	c.setTranslationFields(t, c.tr[0].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt, "locked": true,
	})
	c.seedAIComment(t, c.tr[0].ID, "AI remark on a locked row")
	c.setTranslationFields(t, c.tr[1].ID, db.TranslationStatusNeedsReview, map[string]any{
		"active_text": &mt,
	})
	c.seedAIComment(t, c.tr[1].ID, "AI remark on a clearable row")

	rec := testutil.DoRequest(t, c.router, "POST",
		fmt.Sprintf("/api/jobs/%d/clear-translations", c.job.ID),
		map[string]any{"scope": "all"}, c.userID, c.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	c.assertComments(t, c.tr[0].ID, "locked row", "AI remark on a locked row")
	c.assertComments(t, c.tr[1].ID, "cleared row")
}
