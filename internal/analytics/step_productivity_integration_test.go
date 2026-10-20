package analytics_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationStepProductivity_AttributesPerStepAndDedups pins that
// GetStepProductivityByProject aggregates translation_history (not
// translations.edited_by), so each workflow step's contribution is credited to
// the editor who did it — even after a later step touches the same segment.
//
// It also verifies the CTE de-dup: a segment confirmed twice within the same
// (step, user) counts ONCE toward words_edited but sums ALL its editing-time
// events. Without the per-translation CTE, words would double-count.
//
// Anti-tautology: if the query grouped on translations.edited_by (the old
// report) instead of history.job_step_assignment_id, the translator's step
// would vanish once the reviewer edited tr1 — this test would see one step, not
// two. And if the CTE collapse were dropped, translator words would be 13 (8 +
// the re-counted 5), not 8.
func TestIntegrationStepProductivity_AttributesPerStepAndDedups(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ProdCorp")
	translator := testutil.SeedUser(t, q, tenant.ID, "translator@test.com", "pass")
	reviewer := testutil.SeedUser(t, q, tenant.ID, "reviewer@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "ProdProject", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// The project's settings profile owns the workflow steps.
	profileID := project.SettingsProfileID

	// Two workflow steps on the profile.
	var step1ID, step2ID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required) VALUES ($1, 'Translation', 1, 'member') RETURNING id`,
		profileID).Scan(&step1ID); err != nil {
		t.Fatalf("insert step1: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required) VALUES ($1, 'Review', 2, 'member') RETURNING id`,
		profileID).Scan(&step2ID); err != nil {
		t.Fatalf("insert step2: %v", err)
	}

	// Step assignments for this job (status is irrelevant to the report — it
	// joins by id, not by in_progress).
	var jsa1, jsa2 int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status) VALUES ($1, $2, $3, 'completed') RETURNING id`,
		job.ID, step1ID, translator.ID).Scan(&jsa1); err != nil {
		t.Fatalf("insert jsa1: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status) VALUES ($1, $2, $3, 'in_progress') RETURNING id`,
		job.ID, step2ID, reviewer.ID).Scan(&jsa2); err != nil {
		t.Fatalf("insert jsa2: %v", err)
	}

	// Segments with explicit word counts (SeedSegment leaves word_count 0).
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello world here we go", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Bye now", 2)
	for _, sw := range []struct {
		id int64
		wc int32
	}{{seg1.ID, 5}, {seg2.ID, 3}} {
		if _, err := pool.Exec(ctx, `UPDATE segments SET word_count = $1 WHERE id = $2`, sw.wc, sw.id); err != nil {
			t.Fatalf("set word_count: %v", err)
		}
	}
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "fr", db.TranslationStatusApproved)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "fr", db.TranslationStatusApproved)

	// History events. Translator (step 1) confirmed tr1 twice (dedup case) and
	// tr2 once; reviewer (step 2) later re-confirmed tr1.
	emit := func(trID, stepID, userID int64, timeMs int64, editDist int32) {
		t.Helper()
		td := userID // copy for addressing
		ts := stepID
		tm := timeMs
		ed := editDist
		if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID:       trID,
			Kind:                db.TranslationHistoryKindHuman,
			CreatedBy:           &td,
			EditingTimeMs:       &tm,
			EditDistance:        &ed,
			JobStepAssignmentID: &ts,
		}); err != nil {
			t.Fatalf("emit history: %v", err)
		}
	}
	// A confirm that changed nothing: kind `approved`, and — as in production —
	// no text and no edit distance.
	emitApproved := func(trID, stepID, userID int64, timeMs int64) {
		t.Helper()
		td := userID
		ts := stepID
		tm := timeMs
		if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID:       trID,
			Kind:                db.TranslationHistoryKindApproved,
			CreatedBy:           &td,
			EditingTimeMs:       &tm,
			JobStepAssignmentID: &ts,
		}); err != nil {
			t.Fatalf("emit approved history: %v", err)
		}
	}
	emit(tr1.ID, jsa1, translator.ID, 60000, 10)
	emit(tr1.ID, jsa1, translator.ID, 20000, 4) // same segment+step → dedup words, sum time
	emit(tr2.ID, jsa1, translator.ID, 30000, 6)
	emit(tr1.ID, jsa2, reviewer.ID, 10000, 2)
	// The reviewer read tr2, agreed with it and confirmed. That is this step's
	// work and the report must show it, which is what holds
	// `kind IN ('human','approved')` in place.
	emitApproved(tr2.ID, jsa2, reviewer.ID, 5000)

	rows, err := q.GetStepProductivityByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetStepProductivityByProject: %v", err)
	}

	type key struct {
		step string
		user int64
	}
	got := map[key]db.GetStepProductivityByProjectRow{}
	for _, r := range rows {
		if r.UserID == nil {
			t.Fatalf("unexpected nil user_id in row %+v", r)
		}
		got[key{r.StepName, *r.UserID}] = r
	}

	tRow, ok := got[key{"Translation", translator.ID}]
	if !ok {
		t.Fatalf("missing Translation/translator row; got %+v", got)
	}
	if tRow.SegmentsEdited != 2 {
		t.Errorf("translator segments: got %d, want 2 (tr1 de-duped + tr2)", tRow.SegmentsEdited)
	}
	if tRow.WordsEdited != 8 {
		t.Errorf("translator words: got %d, want 8 (5+3, tr1 counted once)", tRow.WordsEdited)
	}
	if tRow.TotalEditingTimeMs != 110000 {
		t.Errorf("translator time: got %d, want 110000 (60000+20000+30000)", tRow.TotalEditingTimeMs)
	}
	// avg over per-translation averages: tr1 is avg(10,4)=7, tr2 is 6.
	if tRow.AvgEditDistance != 6.5 {
		t.Errorf("translator avg edit distance: got %v, want 6.5", tRow.AvgEditDistance)
	}

	rRow, ok := got[key{"Review", reviewer.ID}]
	if !ok {
		t.Fatalf("missing Review/reviewer row; got %+v", got)
	}
	if rRow.SegmentsEdited != 2 {
		t.Errorf("reviewer segments: got %d, want 2 (tr1 edited + tr2 confirmed unchanged)", rRow.SegmentsEdited)
	}
	if rRow.WordsEdited != 8 {
		t.Errorf("reviewer words: got %d, want 8 (5+3)", rRow.WordsEdited)
	}
	if rRow.TotalEditingTimeMs != 15000 {
		t.Errorf("reviewer time: got %d, want 15000 (10000+5000)", rRow.TotalEditingTimeMs)
	}
	// 2, not 1: the `approved` row carries no distance, and AVG ignores NULLs,
	// so counting the confirm must not halve the reviewer's rewriting figure.
	if rRow.AvgEditDistance != 2 {
		t.Errorf("reviewer avg edit distance: got %v, want 2 (the approved row adds no 0)", rRow.AvgEditDistance)
	}

	// The flat (last-editor) report would credit tr1 only to the reviewer; the
	// per-step report keeps the translator's tr1 contribution visible.
	if len(got) != 2 {
		t.Errorf("expected exactly 2 (step,user) rows, got %d: %+v", len(got), got)
	}
}

// TestIntegrationStepProductivity_Endpoint exercises the HTTP route
// GET /api/projects/{id}/productivity/by-step: correct shape
// (step identity + metrics) and tenant isolation (cross-tenant → 404, same
// posture as the flat productivity endpoint).
func TestIntegrationStepProductivity_Endpoint(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "EndpointCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "ep@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "EP Project", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	profileID := project.SettingsProfileID
	var stepID, jsaID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required) VALUES ($1, 'Translation', 1, 'member') RETURNING id`,
		profileID).Scan(&stepID); err != nil {
		t.Fatalf("step: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status) VALUES ($1, $2, $3, 'in_progress') RETURNING id`,
		job.ID, stepID, user.ID).Scan(&jsaID); err != nil {
		t.Fatalf("jsa: %v", err)
	}

	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	if _, err := pool.Exec(ctx, `UPDATE segments SET word_count = 2 WHERE id = $1`, seg.ID); err != nil {
		t.Fatalf("word_count: %v", err)
	}
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusApproved)
	td, ts, tm := user.ID, jsaID, int64(30000)
	if _, err := q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID:       tr.ID,
		Kind:                db.TranslationHistoryKindHuman,
		CreatedBy:           &td,
		EditingTimeMs:       &tm,
		JobStepAssignmentID: &ts,
	}); err != nil {
		t.Fatalf("history: %v", err)
	}

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/productivity/by-step", project.ID), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data []struct {
			StepAssignmentID int64  `json:"step_assignment_id"`
			StepName         string `json:"step_name"`
			UserID           *int64 `json:"user_id"`
			SegmentsEdited   int32  `json:"segments_edited"`
			WordsEdited      int32  `json:"words_edited"`
			TotalTimeMs      int64  `json:"total_editing_time_ms"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 step row, got %d: %+v", len(resp.Data), resp.Data)
	}
	row := resp.Data[0]
	if row.StepName != "Translation" || row.StepAssignmentID != jsaID {
		t.Errorf("step identity: got %q/%d, want Translation/%d", row.StepName, row.StepAssignmentID, jsaID)
	}
	if row.SegmentsEdited != 1 || row.WordsEdited != 2 || row.TotalTimeMs != 30000 {
		t.Errorf("metrics: got segs=%d words=%d time=%d, want 1/2/30000", row.SegmentsEdited, row.WordsEdited, row.TotalTimeMs)
	}

	// Cross-tenant: another tenant's user must not read this project's report.
	otherTenant := testutil.SeedTenant(t, q, "OtherCorp")
	otherUser := testutil.SeedUser(t, q, otherTenant.ID, "other@test.com", "pass")
	rec = testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/productivity/by-step", project.ID), nil, otherUser.ID, otherTenant.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// TestIntegrationProductivity_CrossTenantReturns404 — a caller in tenant B
// cannot read tenant A's project productivity, even with a valid project ID.
func TestIntegrationProductivity_CrossTenantReturns404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	tenantA := testutil.SeedTenant(t, q, "TA")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "PA", "en", []string{"es"})

	tenantB := testutil.SeedTenant(t, q, "TB")
	userB := testutil.SeedUser(t, q, tenantB.ID, "tb@test.com", "pass")

	rec := testutil.DoRequest(t, router, "GET",
		fmt.Sprintf("/api/projects/%d/productivity", projectA.ID),
		nil, userB.ID, tenantB.ID)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

// TestIntegrationStepProductivity_ReadsOnlyTheProjectsHistory pins that the
// per-step report reads only the project's own translation_history rows.
// Another tenant holds 3000 human history rows; the report on a project with 2
// rows must read a handful of history rows, not the whole table.
func TestIntegrationStepProductivity_ReadsOnlyTheProjectsHistory(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	seedStep := func(tenantName, email string) (projectID, jsaID, segID, userID int64) {
		t.Helper()
		tenant := testutil.SeedTenant(t, q, tenantName)
		user := testutil.SeedUser(t, q, tenant.ID, email, "pass")
		project := testutil.SeedProject(t, q, tenant.ID, tenantName+" project", "en", []string{"fr"})
		file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, "fr")
		profileID := project.SettingsProfileID
		var stepID int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO workflow_steps (settings_profile_id, name, step_order, role_required) VALUES ($1, 'Translation', 1, 'member') RETURNING id`,
			profileID).Scan(&stepID); err != nil {
			t.Fatalf("insert step: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO job_step_assignments (job_id, workflow_step_id, assigned_to, status) VALUES ($1, $2, $3, 'in_progress') RETURNING id`,
			job.ID, stepID, user.ID).Scan(&jsaID); err != nil {
			t.Fatalf("insert jsa: %v", err)
		}
		seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
		return project.ID, jsaID, seg.ID, user.ID
	}

	ownProject, ownJSA, ownSeg, ownUser := seedStep("Own", "own@test.com")
	_, otherJSA, otherSeg, otherUser := seedStep("Other", "other@test.com")

	tr := testutil.SeedTranslation(t, q, ownSeg, "fr", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx, `
		INSERT INTO translation_history (translation_id, kind, created_by, job_step_assignment_id)
		VALUES ($1, 'human', $2, $3), ($1, 'approved', $2, $3)`, tr.ID, ownUser, ownJSA); err != nil {
		t.Fatalf("own history: %v", err)
	}
	otherTr := testutil.SeedTranslation(t, q, otherSeg, "fr", db.TranslationStatusApproved)
	if _, err := pool.Exec(ctx, `
		INSERT INTO translation_history (translation_id, kind, created_by, job_step_assignment_id)
		SELECT $1, 'human', $2, $3 FROM generate_series(1, 3000)`, otherTr.ID, otherUser, otherJSA); err != nil {
		t.Fatalf("other history: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE translation_history`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	rows, err := q.GetStepProductivityByProject(ctx, ownProject)
	if err != nil {
		t.Fatalf("GetStepProductivityByProject: %v", err)
	}
	if len(rows) != 1 || rows[0].SegmentsEdited != 1 {
		t.Fatalf("report rows: got %+v, want one row with 1 segment", rows)
	}

	read := testutil.ExplainAnalyze(t, pool, testutil.GeneratedSQL(t, "GetStepProductivityByProject"), ownProject).RowsScanned("translation_history")
	if read > 10 {
		t.Errorf("history rows read: got %.0f, want at most 10 (the project has 2; another tenant has 3000)", read)
	}
}
