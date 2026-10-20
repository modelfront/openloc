package workflow_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// Each step takes away the previous step's approvals, auto-approvals and
// drafts, records how it left each segment, and a reject gives the reopened
// step its own statuses back.

type roundsFixture struct {
	pool     *pgxpool.Pool
	q        db.Querier
	svc      *workflow.Service
	ctx      context.Context
	tenantID int64
	ownerID  int64
	jobID    int64
	fileID   int64
	steps    []int64
	segs     []db.Segment
	enq      *testutil.RecordingEnqueuer
}

func setupRounds(t *testing.T, stepNames ...string) roundsFixture {
	t.Helper()
	specs := make([]testutil.WorkflowStepSpec, len(stepNames))
	for i, n := range stepNames {
		specs[i] = testutil.WorkflowStepSpec{Name: n}
	}
	return setupRoundsSteps(t, specs...)
}

// setupRoundsSteps is setupRounds with step types: a hybrid or ai step gets
// an AI router on a QE+APE engine of the fixture's tenant.
func setupRoundsSteps(t *testing.T, specs ...testutil.WorkflowStepSpec) roundsFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	ownerID, tenantID := testutil.RegisterUser(t, q, "rounds")
	proj := testutil.SeedProject(t, q, tenantID, "Rounds", "en", []string{"es"})
	file := testutil.SeedFile(t, q, proj.ID, "rounds.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "es")

	var segs []db.Segment
	for i, src := range []string{"one", "two", "three", "four"} {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i))
		tr := testutil.SeedTranslation(t, q, seg.ID, "es", db.TranslationStatusNeedsReview)
		mt := "mt " + src
		if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: tr.ID, ActiveText: &mt}); err != nil {
			t.Fatalf("seed MT: %v", err)
		}
		segs = append(segs, seg)
	}

	for i := range specs {
		if specs[i].StepType == "hybrid" || specs[i].StepType == "ai" {
			engine, err := q.CreateAIEngine(ctx, db.CreateAIEngineParams{TenantID: tenantID, Name: "APE " + specs[i].Name, Purpose: "qe_ape"})
			if err != nil {
				t.Fatal(err)
			}
			router := testutil.SeedRouterForEngine(t, q, tenantID, engine.ID)
			specs[i].RouterID = &router
		}
	}
	specs[0].Status = "in_progress"
	steps := testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID, specs...)
	// Upload opens the first round.
	if err := stepround.Open(ctx, q, job.ID, steps[0], stepround.Keep); err != nil {
		t.Fatalf("open first round: %v", err)
	}
	enq := &testutil.RecordingEnqueuer{}
	return roundsFixture{
		pool: pool, q: q, svc: workflow.NewService(q, pipeline.NewPgxTxRunner(pool), nil).WithAIStep(enq), ctx: ctx,
		tenantID: tenantID, ownerID: ownerID, jobID: job.ID, fileID: file.ID, steps: steps, segs: segs, enq: enq,
	}
}

func (f roundsFixture) tr(t *testing.T, i int) db.Translation {
	t.Helper()
	tr, err := f.q.GetTranslationBySegmentAndLang(f.ctx, db.GetTranslationBySegmentAndLangParams{
		SegmentID: f.segs[i].ID, TargetLanguage: "es",
	})
	if err != nil {
		t.Fatalf("get translation %d: %v", i, err)
	}
	return tr
}

func (f roundsFixture) confirm(t *testing.T, i int, text string) {
	t.Helper()
	if _, err := f.q.UpdateTranslationHuman(f.ctx, db.UpdateTranslationHumanParams{
		ID: f.tr(t, i).ID, HumanText: &text, EditedBy: &f.ownerID,
	}); err != nil {
		t.Fatalf("confirm %d: %v", i, err)
	}
}

func (f roundsFixture) draft(t *testing.T, i int, text string) {
	t.Helper()
	if _, err := f.q.SaveTranslationDraft(f.ctx, db.SaveTranslationDraftParams{
		ID: f.tr(t, i).ID, HumanText: &text, EditedBy: &f.ownerID,
	}); err != nil {
		t.Fatalf("draft %d: %v", i, err)
	}
}

func (f roundsFixture) advance(t *testing.T) workflow.AdvanceResult {
	t.Helper()
	active, err := f.q.GetActiveStepForJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("active step: %v", err)
	}
	res, err := f.svc.AdvanceJob(f.ctx, f.tenantID, f.jobID, active, &f.ownerID, nil, false, nil)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	return res
}

func (f roundsFixture) reject(t *testing.T) {
	t.Helper()
	if _, err := f.svc.Reject(f.ctx, f.tenantID, f.jobID, workflow.Actor{UserID: f.ownerID, Role: "owner"}, workflow.RejectOpts{Reason: "fix it"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
}

func (f roundsFixture) stepStatuses(t *testing.T, i int) []db.ListTranslationStepStatusesRow {
	t.Helper()
	rows, err := f.q.ListTranslationStepStatuses(f.ctx, db.ListTranslationStepStatusesParams{
		TranslationID: f.tr(t, i).ID, TenantID: f.tenantID,
	})
	if err != nil {
		t.Fatalf("list step statuses: %v", err)
	}
	return rows
}

func (f roundsFixture) round(t *testing.T, assignmentID int64) int32 {
	t.Helper()
	rows, err := f.q.ListJobStepAssignments(f.ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == assignmentID {
			return r.Round
		}
	}
	t.Fatalf("assignment %d not on job", assignmentID)
	return 0
}

func wantStatus(t *testing.T, label string, got db.TranslationStatus, want db.TranslationStatus) {
	t.Helper()
	if got != want {
		t.Errorf("%s: status = %s, want %s", label, got, want)
	}
}

func TestIntegrationStepRounds_AdvanceResetsAndRecords(t *testing.T) {
	f := setupRounds(t, "Translation", "Review")

	// Step 1: 0 edited + confirmed, 1 locked + confirmed, 2 auto-approved by
	// QE, 3 untouched.
	f.confirm(t, 0, "uno")
	f.confirm(t, 1, "dos")
	if err := f.q.LockTranslation(f.ctx, f.tr(t, 1).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.UpdateTranslationPredict(f.ctx, db.UpdateTranslationPredictParams{
		ID: f.tr(t, 2).ID, ActiveText: f.tr(t, 2).ActiveText,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true},
		Status:         db.TranslationStatusAutoApproved,
	}); err != nil {
		t.Fatalf("predict: %v", err)
	}
	if !f.tr(t, 0).StepEdited {
		t.Fatal("a confirm that changed the text must mark the segment edited in this step")
	}

	f.advance(t)

	// Step 2 takes away the approvals, auto-approvals and drafts; locked
	// segments keep their status.
	wantStatus(t, "edited+confirmed", f.tr(t, 0).Status, db.TranslationStatusNeedsReview)
	wantStatus(t, "locked", f.tr(t, 1).Status, db.TranslationStatusApproved)
	wantStatus(t, "auto-approved", f.tr(t, 2).Status, db.TranslationStatusNeedsReview)
	if f.tr(t, 2).PredictOutcome.PredictOutcome != db.PredictOutcomeApproved {
		t.Error("the reset keeps the AI verdict, so the next step sees it came from an earlier step")
	}
	if f.tr(t, 0).StepEdited {
		t.Error("step_edited must reset when the next step starts")
	}

	// Step 1's round is on record, per segment.
	s0 := f.stepStatuses(t, 0)
	if len(s0) != 1 || s0[0].Status.TranslationStatus != db.TranslationStatusApproved ||
		s0[0].HumanEdited == nil || !*s0[0].HumanEdited || s0[0].Round != 1 ||
		s0[0].EndReason == nil || *s0[0].EndReason != stepround.Completed || s0[0].StepName != "Translation" {
		t.Errorf("segment 0 step 1 record = %+v, want approved, edited, round 1, completed", s0)
	}
	s2 := f.stepStatuses(t, 2)
	if len(s2) != 1 || s2[0].Status.TranslationStatus != db.TranslationStatusAutoApproved ||
		s2[0].PredictOutcome.PredictOutcome != db.PredictOutcomeApproved || *s2[0].HumanEdited {
		t.Errorf("segment 2 step 1 record = %+v, want auto-approved by QE, not edited", s2)
	}
	s3 := f.stepStatuses(t, 3)
	if len(s3) != 1 || s3[0].Status.TranslationStatus != db.TranslationStatusNeedsReview || *s3[0].HumanEdited {
		t.Errorf("segment 3 step 1 record = %+v, want needs_review, not edited", s3)
	}

	// The record is the tenant's: another tenant reads nothing.
	other, err := f.q.ListTranslationStepStatuses(f.ctx, db.ListTranslationStepStatusesParams{
		TranslationID: f.tr(t, 0).ID, TenantID: f.tenantID + 999,
	})
	if err != nil || len(other) != 0 {
		t.Errorf("another tenant read %d step records (err %v), want 0", len(other), err)
	}

	// The editor's previous-step view of the job.
	prev, err := f.q.ListJobPreviousStepStatuses(f.ctx, db.ListJobPreviousStepStatusesParams{JobID: f.jobID, TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 4 || prev[0].StepName != "Translation" {
		t.Errorf("previous-step rows = %+v, want 4 rows from Translation", prev)
	}
	if again, _ := f.q.ListJobPreviousStepStatuses(f.ctx, db.ListJobPreviousStepStatusesParams{JobID: f.jobID, TenantID: f.tenantID + 999}); len(again) != 0 {
		t.Errorf("another tenant read %d previous-step rows, want 0", len(again))
	}

	// Confirmed in step 1, not yet in step 2: nothing for TM.
	tm, err := f.q.ListApprovedForTM(f.ctx, db.ListApprovedForTMParams{FileID: f.fileID, TargetLanguage: "es"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tm {
		if r.ID == f.tr(t, 0).ID {
			t.Error("a segment the last step has not confirmed must not reach TM")
		}
	}

	// Step 2 (last): the reviewer confirms 0 without changing it.
	f.confirm(t, 0, "uno")
	if f.tr(t, 0).StepEdited {
		t.Error("a confirm with unchanged text is not an edit")
	}
	if res := f.advance(t); !res.WorkflowCompleted {
		t.Fatal("advancing the last step must complete the workflow")
	}

	// The last step leaves its statuses in place.
	wantStatus(t, "reviewed", f.tr(t, 0).Status, db.TranslationStatusApproved)
	wantStatus(t, "auto-approved in step 1, untouched", f.tr(t, 2).Status, db.TranslationStatusNeedsReview)
	s0 = f.stepStatuses(t, 0)
	if len(s0) != 2 || s0[1].StepName != "Review" || s0[1].Status.TranslationStatus != db.TranslationStatusApproved || *s0[1].HumanEdited {
		t.Errorf("segment 0 records = %+v, want Review: approved, not edited", s0)
	}
	if s0[1].PredictOutcome.Valid {
		t.Error("the QE outcome belongs to the step that produced it, not to later ones")
	}
	if got := f.round(t, f.steps[0]); got != 1 {
		t.Errorf("step 1 round = %d, want 1", got)
	}
	if got := f.round(t, f.steps[1]); got != 1 {
		t.Errorf("step 2 round = %d, want 1", got)
	}
}

func TestIntegrationStepRounds_RejectRestores(t *testing.T) {
	f := setupRounds(t, "Translation", "Review")

	// The translator leaves: 0 and 1 approved, 2 as a draft, 3 rejected by
	// the AI check, all in Translation.
	f.confirm(t, 0, "uno")
	f.confirm(t, 1, "dos")
	f.draft(t, 2, "tres, borrador")
	if _, err := f.q.UpdateTranslationPredict(f.ctx, db.UpdateTranslationPredictParams{
		ID: f.tr(t, 3).ID, ActiveText: f.tr(t, 3).ActiveText,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeRejected, Valid: true},
		Status:         db.TranslationStatusNeedsReview,
	}); err != nil {
		t.Fatalf("predict: %v", err)
	}
	f.advance(t)

	// The reviewer rewrites 1 and sends the job back.
	f.draft(t, 1, "dos, corregido")
	f.reject(t)

	// Every segment looks as the translator left it, except the one the
	// reviewer rewrote: that one is unconfirmed and edited, a draft to look at.
	wantStatus(t, "approved, untouched by reviewer", f.tr(t, 0).Status, db.TranslationStatusApproved)
	if !f.tr(t, 0).StepEdited {
		t.Error("segment 0 was edited in the translator's round; the mark comes back with it")
	}
	wantStatus(t, "rewritten by reviewer", f.tr(t, 1).Status, db.TranslationStatusNeedsReview)
	if !f.tr(t, 1).StepEdited {
		t.Error("a segment the reviewer rewrote must come back as a draft")
	}
	wantStatus(t, "translator's draft", f.tr(t, 2).Status, db.TranslationStatusNeedsReview)
	if !f.tr(t, 2).StepEdited {
		t.Error("the translator's draft must come back as a draft")
	}
	before, err := f.q.ListJobPredictedBeforeStep(f.ctx, db.ListJobPredictedBeforeStepParams{JobID: f.jobID, TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range before {
		if id == f.tr(t, 3).ID {
			t.Error("the AI verdict from the translator's own step must still count as this step's")
		}
	}
	if got := f.round(t, f.steps[0]); got != 2 {
		t.Errorf("step 1 round = %d, want 2 after the reject", got)
	}

	// The reviewer's round is on record as rejected.
	s1 := f.stepStatuses(t, 1)
	if len(s1) != 2 || s1[1].StepName != "Review" || *s1[1].EndReason != stepround.Rejected || !*s1[1].HumanEdited {
		t.Errorf("segment 1 records = %+v, want Review rejected, edited", s1)
	}

	// Back to review: a second round of both steps.
	f.advance(t)
	if got := f.round(t, f.steps[1]); got != 2 {
		t.Errorf("step 2 round = %d, want 2", got)
	}
	wantStatus(t, "second review", f.tr(t, 0).Status, db.TranslationStatusNeedsReview)
	s0 := f.stepStatuses(t, 0)
	if len(s0) != 3 || s0[2].StepName != "Translation" || s0[2].Round != 2 {
		t.Errorf("segment 0 records = %+v, want Translation round 2 last", s0)
	}
}

// A locked segment is Approved or Auto-approved and keeps that status across
// every step boundary: the next step, a reject back, and the last step.
func TestIntegrationStepRounds_LockedKeepsStatusEveryBoundary(t *testing.T) {
	f := setupRounds(t, "Translation", "Review", "Proofread")
	f.draft(t, 0, "uno, borrador")
	f.confirm(t, 1, "dos")
	if _, err := f.q.UpdateTranslationPredict(f.ctx, db.UpdateTranslationPredictParams{
		ID: f.tr(t, 2).ID, ActiveText: f.tr(t, 2).ActiveText,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true},
		Status:         db.TranslationStatusAutoApproved,
	}); err != nil {
		t.Fatalf("predict: %v", err)
	}
	f.advance(t)
	wantStatus(t, "confirmed in step 1, reset by step 2", f.tr(t, 1).Status, db.TranslationStatusNeedsReview)

	// The reviewer's manager locks the draft, the reset segment and the
	// auto-approved one.
	if _, err := f.q.LockApproveTranslations(f.ctx, db.LockApproveTranslationsParams{
		ConfirmedVia: "user", FileID: f.fileID, TargetLanguage: "es",
		Ids: []int64{f.tr(t, 0).ID, f.tr(t, 1).ID, f.tr(t, 2).ID},
	}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	check := func(label string) {
		t.Helper()
		for i, want := range []db.TranslationStatus{
			db.TranslationStatusApproved, db.TranslationStatusApproved, db.TranslationStatusAutoApproved,
		} {
			got := f.tr(t, i)
			if !got.Locked || got.Status != want {
				t.Errorf("%s, segment %d: locked=%v status=%s, want locked %s", label, i, got.Locked, got.Status, want)
			}
		}
	}
	check("locked in step 2")

	f.reject(t)
	check("rejected back to step 1")
	f.advance(t)
	check("step 2 again")
	f.advance(t)
	check("step 3")
	if res := f.advance(t); !res.WorkflowCompleted {
		t.Fatal("expected the workflow to complete")
	}
	check("workflow completed")
}
