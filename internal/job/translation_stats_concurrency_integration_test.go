package job_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// statsJob seeds a project with one in-progress job of n translations and
// returns the fixture, the job and its translation ids.
func statsJob(t *testing.T, pool *pgxpool.Pool, q db.Querier, name string, n int) (statsFixture, db.Job, []int64) {
	t.Helper()
	tenant := testutil.SeedTenant(t, q, name)
	project := testutil.SeedProject(t, q, tenant.ID, name, "en", []string{"fr"})
	f := statsFixture{pool: pool, q: q, tenantID: tenant.ID, projectID: project.ID, profileID: project.SettingsProfileID}
	f.exec(t, `UPDATE projects SET status = 'in_progress' WHERE id = $1`, project.ID)
	file := testutil.SeedFile(t, q, project.ID, "c.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")
	ids := testutil.SeedSegmentsBulk(t, pool, file.ID, "fr", 0, n, "Texte")
	return f, job, ids
}

// waitDone fails the test when done does not deliver within 10 seconds, and
// fails it with the error it delivers.
func waitDone(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: still blocked after 10s", what)
	}
}

// TestIntegrationJobTranslationStats_ConcurrentRecountsCountOnce pins that two
// recounts of one job at the same time add the missing rows once: the second
// waits for the first and then sees its rows.
func TestIntegrationJobTranslationStats_ConcurrentRecountsCountOnce(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	f, job, _ := statsJob(t, pool, q, "RecountCorp", 20)

	// The counts lost the job's rows; both recounts must put them back once.
	f.exec(t, `DELETE FROM job_translation_stats WHERE file_id = $1`, job.FileID)
	const recount = `SELECT job_translation_stats_correct(ARRAY[$1::bigint], ARRAY['fr'])`

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, recount, job.FileID); err != nil {
		t.Fatalf("first recount: %v", err)
	}
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin second: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	pid2 := testutil.BackendPID(t, tx2)
	done := make(chan error, 1)
	go func() {
		if _, err := tx2.Exec(ctx, recount, job.FileID); err != nil {
			done <- err
			return
		}
		done <- tx2.Commit(ctx)
	}()
	testutil.WaitLockWait(t, pool, pid2)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit first recount: %v", err)
	}
	waitDone(t, done, "second recount")
	f.check(t, "two concurrent recounts")
}

// raceSetup seeds a job on its first step whose segment x started that round
// with an AI verdict that has since been cleared, then opens a transaction
// that moves the job to its second step without committing. An AI verdict on
// x counts as this round's under the second step and not under the first.
func raceSetup(t *testing.T, name string) (statsFixture, pgx.Tx, int64) {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	f, job, ids := statsJob(t, pool, q, name, 5)
	x := ids[0]

	user := testutil.SeedUser(t, q, f.tenantID, strings.ToLower(name)+"@test.com", "pass")
	steps := testutil.SeedWorkflow(t, q, f.profileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &user.ID},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &user.ID})
	f.exec(t, `UPDATE translations SET predict_outcome = 'rejected' WHERE id = $1`, x)
	f.inTx(t, func(tq db.Querier) error { return stepround.Open(ctx, tq, job.ID, steps[0], stepround.Keep) })
	f.exec(t, `UPDATE translations SET predict_outcome = NULL WHERE id = $1`, x)
	f.check(t, "before the step change")

	stepTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = stepTx.Rollback(ctx) })
	sq := db.New(stepTx)
	if err := sq.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &steps[1]}); err != nil {
		t.Fatalf("SetJobCurrentStep: %v", err)
	}
	if err := stepround.Open(ctx, sq, job.ID, steps[1], stepround.Keep); err != nil {
		t.Fatalf("open step round: %v", err)
	}
	return f, stepTx, x
}

func predictApproved(ctx context.Context, q db.Querier, id int64) error {
	_, err := q.UpdateTranslationPredict(ctx, db.UpdateTranslationPredictParams{
		ID: id, ActiveText: strp("Texte"), Status: db.TranslationStatusNeedsReview,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeApproved, Valid: true},
	})
	return err
}

// TestIntegrationJobTranslationStats_WriteDuringStepChange pins that a
// translation write committed while its job changes step is counted under
// the step the job ends on.
func TestIntegrationJobTranslationStats_WriteDuringStepChange(t *testing.T) {
	ctx := context.Background()
	f, stepTx, x := raceSetup(t, "RaceCorp")

	if err := predictApproved(ctx, f.q, x); err != nil {
		t.Fatalf("AI verdict write: %v", err)
	}
	if err := stepTx.Commit(ctx); err != nil {
		t.Fatalf("commit step change: %v", err)
	}
	f.check(t, "AI verdict committed during a step change")
}

// TestIntegrationJobTranslationStats_WriteOpenAcrossStepCommit pins the same
// when the write is still open as the step change commits: the commit waits
// for it, then counts it.
func TestIntegrationJobTranslationStats_WriteOpenAcrossStepCommit(t *testing.T) {
	ctx := context.Background()
	f, stepTx, x := raceSetup(t, "OpenRaceCorp")

	writeTx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin write: %v", err)
	}
	defer func() { _ = writeTx.Rollback(ctx) }()
	if err := predictApproved(ctx, db.New(writeTx), x); err != nil {
		t.Fatalf("AI verdict write: %v", err)
	}
	stepPID := testutil.BackendPID(t, stepTx)
	done := make(chan error, 1)
	go func() { done <- stepTx.Commit(ctx) }()
	testutil.WaitLockWait(t, f.pool, stepPID)
	if err := writeTx.Commit(ctx); err != nil {
		t.Fatalf("commit write: %v", err)
	}
	waitDone(t, done, "step change commit")
	f.check(t, "AI verdict open across the step commit")
}

// TestIntegrationJobTranslationStats_DeleteDuringStepCommit pins that a job
// delete holding the job's translations and a step change committing on the
// same job take their locks in one order: the commit waits for the delete
// instead of the two deadlocking. The step change only moves the current
// step; opening a round would key-share-lock every translation, and the
// delete would then wait for the step change before taking any row.
func TestIntegrationJobTranslationStats_DeleteDuringStepCommit(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	f, job, _ := statsJob(t, pool, q, "DeleteRaceCorp", 5)
	user := testutil.SeedUser(t, q, f.tenantID, "deleterace@test.com", "pass")
	steps := testutil.SeedWorkflow(t, q, f.profileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &user.ID},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &user.ID})

	stepTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin step: %v", err)
	}
	defer func() { _ = stepTx.Rollback(ctx) }()
	if err := db.New(stepTx).SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &steps[1]}); err != nil {
		t.Fatalf("SetJobCurrentStep: %v", err)
	}

	delTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin delete: %v", err)
	}
	defer func() { _ = delTx.Rollback(ctx) }()
	// The delete holds the job's rows before its statement trigger runs.
	if _, err := delTx.Exec(ctx, `
		SELECT 1 FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = $2 FOR UPDATE OF t`, job.FileID, job.TargetLanguage); err != nil {
		t.Fatalf("lock rows: %v", err)
	}
	stepPID := testutil.BackendPID(t, stepTx)
	done := make(chan error, 1)
	go func() { done <- stepTx.Commit(ctx) }()
	testutil.WaitLockWait(t, pool, stepPID)

	dq := db.New(delTx)
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := dq.DeleteJobTranslations(dctx, db.DeleteJobTranslationsParams{FileID: job.FileID, TargetLanguage: job.TargetLanguage}); err != nil {
		t.Fatalf("DeleteJobTranslations: %v", err)
	}
	if err := delTx.Commit(ctx); err != nil {
		t.Fatalf("commit delete: %v", err)
	}
	waitDone(t, done, "step change commit")
	f.check(t, "job translations deleted during a step commit")
}

// TestIntegrationJobTranslationStats_TwoLanguageStepCommitVsJobDelete pins
// that a transaction changing the step of two languages of one file does not
// deadlock with a delete on one of those jobs: the step commit recounts the
// first language, waits for the second language's rows, and the delete's own
// recount of that language goes through meanwhile.
func TestIntegrationJobTranslationStats_TwoLanguageStepCommitVsJobDelete(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "TwoLangCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "twolang@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "TwoLang", "en", []string{"de", "es"})
	f := statsFixture{pool: pool, q: q, tenantID: tenant.ID, projectID: project.ID}
	file := testutil.SeedFile(t, q, project.ID, "t.txt", db.FileFormatTxt)
	testutil.SeedSegmentsBulk(t, pool, file.ID, "de", 0, 5, "Text")
	f.exec(t, `INSERT INTO translations (segment_id, target_language, status, active_text)
		SELECT id, 'es', 'needs_review', 'Texto' FROM segments WHERE file_id = $1`, file.ID)
	profileID := project.SettingsProfileID
	type langJob struct {
		job   db.Job
		steps []int64
	}
	jobs := map[string]langJob{}
	for _, lang := range []string{"de", "es"} {
		job := testutil.SeedJob(t, q, file.ID, lang)
		steps := testutil.SeedWorkflow(t, q, profileID, job.ID,
			testutil.WorkflowStepSpec{Name: "Translation " + lang, Status: "in_progress", Assignee: &user.ID},
			testutil.WorkflowStepSpec{Name: "Review " + lang, Assignee: &user.ID})
		jobs[lang] = langJob{job: job, steps: steps}
	}
	f.check(t, "seeded")

	stepTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin step: %v", err)
	}
	defer func() { _ = stepTx.Rollback(ctx) }()
	for _, lang := range []string{"de", "es"} {
		j := jobs[lang]
		if err := db.New(stepTx).SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: j.job.ID, CurrentStepID: &j.steps[1]}); err != nil {
			t.Fatalf("SetJobCurrentStep %s: %v", lang, err)
		}
	}

	delTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin delete: %v", err)
	}
	defer func() { _ = delTx.Rollback(ctx) }()
	if _, err := delTx.Exec(ctx, `
		SELECT 1 FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'es' FOR UPDATE OF t`, file.ID); err != nil {
		t.Fatalf("lock es rows: %v", err)
	}
	stepPID := testutil.BackendPID(t, stepTx)
	done := make(chan error, 1)
	go func() { done <- stepTx.Commit(ctx) }()
	testutil.WaitLockWait(t, pool, stepPID)

	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.New(delTx).DeleteJobTranslations(dctx, db.DeleteJobTranslationsParams{FileID: file.ID, TargetLanguage: "es"}); err != nil {
		t.Fatalf("DeleteJobTranslations es: %v", err)
	}
	if err := delTx.Commit(ctx); err != nil {
		t.Fatalf("commit delete: %v", err)
	}
	waitDone(t, done, "two-language step commit")
	f.check(t, "es deleted during a two-language step commit")
}

// TestIntegrationJobTranslationStats_ConcurrentWritersOneJob pins that
// writers to one job do not block each other or fail, compaction included:
// two transactions save 40 segments each, interleaved, and commit together.
func TestIntegrationJobTranslationStats_ConcurrentWritersOneJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	f, _, ids := statsJob(t, pool, q, "WritersCorp", 80)
	user := testutil.SeedUser(t, q, f.tenantID, "writers@test.com", "pass")

	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin A: %v", err)
	}
	defer func() { _ = txA.Rollback(ctx) }()
	txB, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin B: %v", err)
	}
	defer func() { _ = txB.Rollback(ctx) }()
	qa, qb := db.New(txA), db.New(txB)
	for i := 0; i < 40; i++ {
		for _, w := range []struct {
			q  db.Querier
			id int64
		}{{qa, ids[i]}, {qb, ids[40+i]}} {
			// A write that waited on the other transaction would hang here.
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := w.q.SaveTranslationDraft(wctx, db.SaveTranslationDraftParams{ID: w.id, HumanText: strp("Brouillon"), EditedBy: &user.ID})
			cancel()
			if err != nil {
				t.Fatalf("save %d: %v", i, err)
			}
		}
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	if err := txB.Commit(ctx); err != nil {
		t.Fatalf("commit B: %v", err)
	}
	f.check(t, "two interleaved writers")
}
