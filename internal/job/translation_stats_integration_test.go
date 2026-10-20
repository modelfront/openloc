package job_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepround"
	"openloc/internal/testutil"
)

// Live recounts over translations: the reference the counts table must match.

const liveFactsSQL = `
	SELECT s.file_id, t.target_language, t.status::text,
	       translation_text_state(t.active_text), t.step_edited, t.edited_by IS NOT NULL,
	       (t.predict_outcome IS NOT NULL AND cur.start_predict_outcome IS NULL), t.locked,
	       count(*)::bigint, sum(s.word_count)::bigint
	FROM translations t
	JOIN segments s ON s.id = t.segment_id
	LEFT JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
	LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
	LEFT JOIN translation_step_statuses cur
	  ON cur.translation_id = t.id AND cur.job_step_assignment_id = jsa.id AND cur.round = jsa.round
	GROUP BY 1, 2, 3, 4, 5, 6, 7, 8`

const storedFactsSQL = `
	SELECT file_id, target_language, status::text, text_state, step_edited, edited, predict_this_round, locked,
	       sum(segments)::bigint, sum(words)::bigint
	FROM job_translation_stats
	GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
	HAVING sum(segments) <> 0 OR sum(words) <> 0`

const liveActivitySQL = `
	SELECT s.file_id, t.target_language, max(t.updated_at)
	FROM translations t JOIN segments s ON s.id = t.segment_id
	GROUP BY 1, 2`

const storedActivitySQL = `
	SELECT file_id, target_language, max(last_activity_at)
	FROM job_translation_stats
	GROUP BY 1, 2`

const liveJobCountsSQL = `
	SELECT j.file_id, j.target_language,
	    COUNT(*)::int,
	    COUNT(*) FILTER (WHERE t.active_text IS NULL)::int,
	    COUNT(*) FILTER (WHERE t.status = 'needs_review' AND t.active_text IS NOT NULL
	        AND NOT t.step_edited
	        AND NOT (t.predict_outcome IS NOT NULL AND cur.start_predict_outcome IS NULL))::int,
	    COUNT(*) FILTER (WHERE t.status = 'needs_review' AND t.active_text IS NOT NULL
	        AND (t.step_edited
	             OR (t.predict_outcome IS NOT NULL AND cur.start_predict_outcome IS NULL)))::int,
	    COUNT(*) FILTER (WHERE t.status = 'auto_approved')::int,
	    COUNT(*) FILTER (WHERE t.status = 'approved')::int,
	    COUNT(*) FILTER (WHERE t.locked = false AND t.active_text IS NOT NULL
	        AND t.status NOT IN ('approved', 'auto_approved'))::int,
	    COUNT(*) FILTER (WHERE t.locked = false AND t.active_text IS NOT NULL)::int
	FROM jobs j
	JOIN files f ON f.id = j.file_id
	JOIN segments s ON s.file_id = j.file_id
	JOIN translations t ON t.segment_id = s.id AND t.target_language = j.target_language
	LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
	LEFT JOIN translation_step_statuses cur
	  ON cur.translation_id = t.id AND cur.job_step_assignment_id = jsa.id AND cur.round = jsa.round
	WHERE f.project_id = $1
	GROUP BY j.file_id, j.target_language`

const liveProgressSQL = `
	SELECT
	    p.id, p.name, t.target_language,
	    count(*)::bigint,
	    COALESCE(sum(s.word_count), 0)::bigint,
	    count(*) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') = '')::bigint,
	    COALESCE(sum(s.word_count) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') = ''), 0)::bigint,
	    count(*) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') <> '' AND t.edited_by IS NULL)::bigint,
	    COALESCE(sum(s.word_count) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') <> '' AND t.edited_by IS NULL), 0)::bigint,
	    count(*) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') <> '' AND t.edited_by IS NOT NULL)::bigint,
	    COALESCE(sum(s.word_count) FILTER (WHERE t.status = 'needs_review' AND COALESCE(t.active_text, '') <> '' AND t.edited_by IS NOT NULL), 0)::bigint,
	    count(*) FILTER (WHERE t.status = 'auto_approved')::bigint,
	    COALESCE(sum(s.word_count) FILTER (WHERE t.status = 'auto_approved'), 0)::bigint,
	    count(*) FILTER (WHERE t.status = 'approved')::bigint,
	    COALESCE(sum(s.word_count) FILTER (WHERE t.status = 'approved'), 0)::bigint
	FROM projects p
	JOIN files f ON f.project_id = p.id
	JOIN segments s ON s.file_id = f.id
	JOIN translations t ON t.segment_id = s.id
	WHERE p.tenant_id = $1 AND p.id = ANY($2::bigint[])
	GROUP BY p.id, p.name, t.target_language
	ORDER BY p.id DESC, t.target_language`

const liveStaleSQL = `
	SELECT j.id, p.id, p.name, f.name, j.target_language, j.status, j.current_step,
	    GREATEST(j.updated_at, COALESCE(te.last_edit, j.updated_at))::timestamptz
	FROM jobs j
	JOIN files f ON f.id = j.file_id
	JOIN projects p ON p.id = f.project_id
	LEFT JOIN LATERAL (
	    SELECT max(t.updated_at) AS last_edit
	    FROM translations t
	    JOIN segments s ON s.id = t.segment_id
	    WHERE s.file_id = j.file_id AND t.target_language = j.target_language
	) te ON true
	WHERE p.tenant_id = $1
	  AND p.status = 'in_progress'
	  AND j.status NOT IN ('completed', 'failed')
	  AND j.updated_at < $2::timestamptz
	  AND GREATEST(j.updated_at, COALESCE(te.last_edit, j.updated_at)) < $2::timestamptz
	ORDER BY 8 ASC, j.id
	LIMIT $3`

// rowStrings runs sql and renders every row as one string, sorted.
func rowStrings(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("query: %v\n%s", err, sql)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		vals, err := r.Values()
		if err != nil {
			return "", err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			if ts, ok := v.(time.Time); ok {
				v = ts.UTC().Format(time.RFC3339Nano)
			}
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, " | "), nil
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	sort.Strings(out)
	return out
}

type statsFixture struct {
	pool      *pgxpool.Pool
	q         db.Querier
	tenantID  int64
	projectID int64
	profileID int64 // the project's settings profile, for SeedWorkflow
}

// check compares the counts table, and every reader built on it, with a live
// recount.
func (f statsFixture) check(t *testing.T, step string) {
	t.Helper()
	ctx := context.Background()
	if got, want := rowStrings(t, f.pool, storedFactsSQL), rowStrings(t, f.pool, liveFactsSQL); !slices.Equal(got, want) {
		t.Fatalf("%s: counts table differs from live recount\nstored: %v\nlive:   %v", step, got, want)
	}

	live := map[string]time.Time{}
	rows, err := f.pool.Query(ctx, liveActivitySQL)
	if err != nil {
		t.Fatalf("%s: live activity: %v", step, err)
	}
	for rows.Next() {
		var file int64
		var lang string
		var at time.Time
		if err := rows.Scan(&file, &lang, &at); err != nil {
			t.Fatalf("scan: %v", err)
		}
		live[fmt.Sprint(file, lang)] = at
	}
	rows.Close()
	rows, err = f.pool.Query(ctx, storedActivitySQL)
	if err != nil {
		t.Fatalf("%s: stored activity: %v", step, err)
	}
	stored := map[string]pgtype.Timestamptz{}
	for rows.Next() {
		var file int64
		var lang string
		var at pgtype.Timestamptz
		if err := rows.Scan(&file, &lang, &at); err != nil {
			t.Fatalf("scan: %v", err)
		}
		stored[fmt.Sprint(file, lang)] = at
	}
	rows.Close()
	for k, want := range live {
		got := stored[k]
		if !got.Valid || !got.Time.Equal(want) {
			t.Fatalf("%s: last activity of job %s: stored %v, live %v", step, k, got.Time, want)
		}
	}

	counts, err := f.q.GetTranslationCountsByProject(ctx, db.GetTranslationCountsByProjectParams{ProjectID: f.projectID})
	if err != nil {
		t.Fatalf("%s: GetTranslationCountsByProject: %v", step, err)
	}
	var gotCounts []string
	for _, c := range counts {
		gotCounts = append(gotCounts, fmt.Sprintf("%d | %s | %d | %d | %d | %d | %d | %d | %d | %d", c.FileID, c.TargetLanguage,
			c.Total, c.Untranslated, c.PreTranslated, c.NeedsReview, c.AutoApproved, c.Approved, c.ClearableNonConfirmed, c.ClearableAll))
		one, err := f.q.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{FileID: c.FileID, TargetLanguage: c.TargetLanguage})
		if err != nil {
			t.Fatalf("%s: GetJobTranslationCounts: %v", step, err)
		}
		batch := db.GetJobTranslationCountsRow{
			Total: c.Total, Untranslated: c.Untranslated, PreTranslated: c.PreTranslated, NeedsReview: c.NeedsReview,
			AutoApproved: c.AutoApproved, Approved: c.Approved,
			ClearableNonConfirmed: c.ClearableNonConfirmed, ClearableAll: c.ClearableAll,
		}
		if one != batch {
			t.Fatalf("%s: GetJobTranslationCounts %+v disagrees with the project batch %+v", step, one, c)
		}
	}
	sort.Strings(gotCounts)
	if want := rowStrings(t, f.pool, liveJobCountsSQL, f.projectID); !slices.Equal(gotCounts, want) {
		t.Fatalf("%s: job counts differ\ntable: %v\nlive:  %v", step, gotCounts, want)
	}

	progress, err := f.q.GetTranslationProgress(ctx, db.GetTranslationProgressParams{TenantID: f.tenantID, ProjectIds: []int64{f.projectID}})
	if err != nil {
		t.Fatalf("%s: GetTranslationProgress: %v", step, err)
	}
	var gotProgress []string
	for _, p := range progress {
		gotProgress = append(gotProgress, fmt.Sprintf("%d | %s | %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d",
			p.ProjectID, p.ProjectName, p.TargetLanguage, p.TotalSegments, p.TotalWords,
			p.UntranslatedSegments, p.UntranslatedWords, p.PreTranslatedSegments, p.PreTranslatedWords,
			p.DraftSegments, p.DraftWords, p.AutoApprovedSegments, p.AutoApprovedWords, p.ConfirmedSegments, p.ConfirmedWords))
	}
	sort.Strings(gotProgress)
	if want := rowStrings(t, f.pool, liveProgressSQL, f.tenantID, []int64{f.projectID}); !slices.Equal(gotProgress, want) {
		t.Fatalf("%s: progress report differs\ntable: %v\nlive:  %v", step, gotProgress, want)
	}

	{
		before := time.Now().Add(time.Hour)
		stale, err := f.q.ListStaleJobs(ctx, db.ListStaleJobsParams{
			TenantID: f.tenantID, RowLimit: 100, Before: pgtype.Timestamptz{Time: before, Valid: true},
		})
		if err != nil {
			t.Fatalf("%s: ListStaleJobs: %v", step, err)
		}
		var gotStale []string
		for _, s := range stale {
			gotStale = append(gotStale, fmt.Sprintf("%d | %d | %s | %s | %s | %v | %v | %s", s.JobID, s.ProjectID, s.ProjectName,
				s.FileName, s.TargetLanguage, s.Status, s.CurrentStep, s.LastActivityAt.Time.UTC().Format(time.RFC3339Nano)))
		}
		sort.Strings(gotStale)
		if want := rowStrings(t, f.pool, liveStaleSQL, f.tenantID, before, 100); !slices.Equal(gotStale, want) {
			t.Fatalf("%s: stuck list differs\ntable: %v\nlive:  %v", step, gotStale, want)
		}
	}
}

// translationIDs returns the job's translation ids in segment order.
func (f statsFixture) translationIDs(t *testing.T, fileID int64, lang string) []int64 {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = $2 ORDER BY s.position`, fileID, lang)
	if err != nil {
		t.Fatalf("translation ids: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("collect ids: %v", err)
	}
	return ids
}

func (f statsFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, sql)
	}
}

// inTx runs fn on one transaction, as the step services do.
func (f statsFixture) inTx(t *testing.T, fn func(q db.Querier) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		t.Fatalf("in tx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func strp(s string) *string { return &s }

// TestIntegrationJobTranslationStats_MatchesLiveRecount drives every kind of
// translation write through the real queries and, after each, compares the
// trigger-kept counts table and all four readers with a live recount.
func TestIntegrationJobTranslationStats_MatchesLiveRecount(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "StatsCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "stats@test.com", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Stats", "en", []string{"fr", "de"})
	f := statsFixture{pool: pool, q: q, tenantID: tenant.ID, projectID: project.ID}
	f.exec(t, `UPDATE projects SET status = 'in_progress' WHERE id = $1`, project.ID)

	fileA := testutil.SeedFile(t, q, project.ID, "a.txt", db.FileFormatTxt)
	fileB := testutil.SeedFile(t, q, project.ID, "b.txt", db.FileFormatTxt)
	jobFr := testutil.SeedJob(t, q, fileA.ID, "fr")
	jobDe := testutil.SeedJob(t, q, fileA.ID, "de")
	jobB := testutil.SeedJob(t, q, fileB.ID, "fr")

	// Bulk insert, a second language in one INSERT … SELECT, untranslated and
	// empty rows, and per-segment word counts.
	testutil.SeedSegmentsBulk(t, pool, fileA.ID, "fr", 0, 30, "Texte")
	f.exec(t, `INSERT INTO translations (segment_id, target_language, status, active_text)
		SELECT id, 'de', 'needs_review', CASE WHEN position % 3 = 0 THEN NULL ELSE 'Text' END
		FROM segments WHERE file_id = $1`, fileA.ID)
	f.exec(t, `UPDATE translations SET active_text = NULL WHERE id IN (
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'fr' AND s.position > 25)`, fileA.ID)
	f.exec(t, `UPDATE translations SET active_text = '' WHERE id IN (
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE s.file_id = $1 AND t.target_language = 'fr' AND s.position = 25)`, fileA.ID)
	f.exec(t, `UPDATE segments SET word_count = position % 7 + 1 WHERE file_id = $1`, fileA.ID)
	f.exec(t, `UPDATE segments SET source_text = 'Same source' WHERE file_id = $1 AND position IN (10, 11, 12)`, fileA.ID)
	testutil.SeedSegmentsBulk(t, pool, fileB.ID, "fr", 0, 10, "Texte")
	f.check(t, "bulk seed")

	fr := f.translationIDs(t, fileA.ID, "fr")

	steps := testutil.SeedWorkflow(t, q, project.SettingsProfileID, jobFr.ID,
		testutil.WorkflowStepSpec{Name: "Translation", Status: "in_progress", Assignee: &user.ID},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &user.ID})
	f.check(t, "job moves onto its first step")
	f.inTx(t, func(tq db.Querier) error { return stepround.Open(ctx, tq, jobFr.ID, steps[0], stepround.Keep) })
	f.check(t, "step round opens")

	// Pipeline writes: MT fill, then AI verdicts produced in this round.
	for _, id := range fr[26:29] {
		if _, err := q.UpdateTranslationMT(ctx, db.UpdateTranslationMTParams{ID: id, ActiveText: strp("MT")}); err != nil {
			t.Fatalf("UpdateTranslationMT: %v", err)
		}
	}
	for i, id := range fr[0:5] {
		outcome, status := db.PredictOutcomeApproved, db.TranslationStatusAutoApproved
		if i >= 3 {
			outcome, status = db.PredictOutcomeRejected, db.TranslationStatusNeedsReview
		}
		if _, err := q.UpdateTranslationPredict(ctx, db.UpdateTranslationPredictParams{
			ID: id, ActiveText: strp("Texte"), Status: status,
			PredictOutcome: db.NullPredictOutcome{PredictOutcome: outcome, Valid: true},
		}); err != nil {
			t.Fatalf("UpdateTranslationPredict: %v", err)
		}
	}
	f.check(t, "pipeline MT and AI verdicts")

	// Human saves: drafts, confirms, repetition propagation.
	for _, id := range fr[5:7] {
		if _, err := q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{ID: id, HumanText: strp("Brouillon"), EditedBy: &user.ID}); err != nil {
			t.Fatalf("SaveTranslationDraft: %v", err)
		}
	}
	for _, id := range fr[7:10] {
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: id, HumanText: strp("Confirmé"), EditedBy: &user.ID}); err != nil {
			t.Fatalf("UpdateTranslationHuman: %v", err)
		}
	}
	if _, err := q.PropagateTranslation(ctx, db.PropagateTranslationParams{
		HumanText: strp("Même"), ActorID: &user.ID, FileID: fileA.ID, SourceText: "Same source",
		TargetLanguage: "fr", ExcludeID: fr[9],
	}); err != nil {
		t.Fatalf("PropagateTranslation: %v", err)
	}
	f.check(t, "human save, confirm, propagation")

	// Lock, unlock, bulk lock-and-approve.
	for _, id := range fr[12:14] {
		if err := q.LockTranslation(ctx, id); err != nil {
			t.Fatalf("LockTranslation: %v", err)
		}
	}
	if err := q.UnlockTranslation(ctx, fr[13]); err != nil {
		t.Fatalf("UnlockTranslation: %v", err)
	}
	if _, err := q.LockApproveTranslations(ctx, db.LockApproveTranslationsParams{
		ConfirmedVia: "user", FileID: fileA.ID, TargetLanguage: "fr", Ids: fr[14:17],
	}); err != nil {
		t.Fatalf("LockApproveTranslations: %v", err)
	}
	f.check(t, "lock, unlock, lock-approve")

	// Clear one segment, then the job's unconfirmed segments.
	if _, err := q.ClearTranslation(ctx, db.ClearTranslationParams{ID: fr[17]}); err != nil {
		t.Fatalf("ClearTranslation: %v", err)
	}
	if _, err := q.ClearTranslationsByJob(ctx, db.ClearTranslationsByJobParams{FileID: fileA.ID, TargetLanguage: "fr", Ids: fr[18:22]}); err != nil {
		t.Fatalf("ClearTranslationsByJob: %v", err)
	}
	f.check(t, "clear")

	// Step advance: close the first step, move on, reset approvals.
	f.inTx(t, func(tq db.Querier) error {
		if err := stepround.Close(ctx, tq, steps[0], stepround.Completed); err != nil {
			return err
		}
		if err := tq.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobFr.ID, CurrentStepID: &steps[1]}); err != nil {
			return err
		}
		return stepround.Open(ctx, tq, jobFr.ID, steps[1], stepround.Reset)
	})
	f.check(t, "step advance")
	if _, err := q.UpdateTranslationPredict(ctx, db.UpdateTranslationPredictParams{
		ID: fr[26], ActiveText: strp("MT"), Status: db.TranslationStatusNeedsReview,
		PredictOutcome: db.NullPredictOutcome{PredictOutcome: db.PredictOutcomeRejected, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateTranslationPredict in review: %v", err)
	}
	f.check(t, "AI verdict in the second step")

	// Rollback: send the job back to the first step and restore its round.
	f.inTx(t, func(tq db.Querier) error {
		if err := stepround.Close(ctx, tq, steps[1], stepround.Rejected); err != nil {
			return err
		}
		if err := tq.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: jobFr.ID, CurrentStepID: &steps[0]}); err != nil {
			return err
		}
		return stepround.Open(ctx, tq, jobFr.ID, steps[0], stepround.Restore)
	})
	f.check(t, "rollback")

	// Many saves on one job: compaction keeps its row count bounded.
	for i := 0; i < 80; i++ {
		if _, err := q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{
			ID: fr[i%5+5], HumanText: strp(fmt.Sprintf("Brouillon %d", i)), EditedBy: &user.ID,
		}); err != nil {
			t.Fatalf("SaveTranslationDraft %d: %v", i, err)
		}
	}
	f.check(t, "80 saves")
	var jobRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_translation_stats WHERE file_id = $1 AND target_language = 'fr'`,
		fileA.ID).Scan(&jobRows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if jobRows > 33 {
		t.Errorf("counts rows of one job after 80 saves: got %d, want at most 33 (compaction)", jobRows)
	}

	// An update that moves updated_at back, as seed and demo data do.
	f.exec(t, `UPDATE translations SET updated_at = now() - interval '10 days' WHERE id IN (
		SELECT t.id FROM translations t JOIN segments s ON s.id = t.segment_id WHERE s.file_id = $1)`, fileB.ID)
	f.check(t, "updated_at moved back")

	// Deletes: a job with its translations (one transaction, as the job
	// delete handler does), a file's segments, a job, then the file.
	f.inTx(t, func(tq db.Querier) error {
		if err := tq.DeleteJobTranslations(ctx, db.DeleteJobTranslationsParams{FileID: fileA.ID, TargetLanguage: "de"}); err != nil {
			return err
		}
		return tq.DeleteJob(ctx, jobDe.ID)
	})
	f.check(t, "delete a job and its translations")
	if err := q.DeleteSegmentsByFile(ctx, fileB.ID); err != nil {
		t.Fatalf("DeleteSegmentsByFile: %v", err)
	}
	f.check(t, "delete a file's segments")
	testutil.SeedSegmentsBulk(t, pool, fileB.ID, "fr", 0, 5, "Neu")
	f.check(t, "re-add segments")
	if err := q.DeleteJob(ctx, jobB.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	f.check(t, "delete a job")
	if err := q.DeleteFile(ctx, fileB.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	f.check(t, "delete a file")
	var leftover int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_translation_stats WHERE file_id = $1`, fileB.ID).Scan(&leftover); err != nil {
		t.Fatalf("count leftover: %v", err)
	}
	if leftover != 0 {
		t.Errorf("counts rows of a deleted file: got %d, want 0", leftover)
	}

	// Repair: drop a job's rows behind the triggers' back, then recount it,
	// and recount everything.
	// One transaction, so a failure cannot leave the trigger off.
	behind, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE translations DISABLE TRIGGER job_translation_stats_upd`,
		fmt.Sprintf(`UPDATE translations SET status = 'approved' WHERE id = %d`, fr[0]),
		`ALTER TABLE translations ENABLE TRIGGER job_translation_stats_upd`,
	} {
		if _, err := behind.Exec(ctx, stmt); err != nil {
			_ = behind.Rollback(ctx)
			t.Fatalf("write behind the trigger: %v", err)
		}
	}
	if err := behind.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	f.exec(t, `DELETE FROM job_translation_stats WHERE file_id = $1 AND status = 'needs_review'`, fileA.ID)
	f.exec(t, `SELECT job_translation_stats_correct(ARRAY[$1::bigint], ARRAY['fr'])`, fileA.ID)
	f.check(t, "repair one job")
	f.exec(t, `TRUNCATE job_translation_stats`)
	f.exec(t, `CALL job_translation_stats_correct_all()`)
	f.check(t, "repair all")
}

// TestIntegrationJobTranslationStats_RepairAllAcrossBatches pins that the
// backfill counts every file once when it takes several batches, cut by file
// count and by segment count.
func TestIntegrationJobTranslationStats_RepairAllAcrossBatches(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "BatchCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Batches", "en", []string{"fr"})
	f := statsFixture{pool: pool, q: q, tenantID: tenant.ID, projectID: project.ID}
	for i, n := range []int{3, 1, 4, 2} {
		file := testutil.SeedFile(t, q, project.ID, fmt.Sprintf("b%d.txt", i), db.FileFormatTxt)
		testutil.SeedJob(t, q, file.ID, "fr")
		testutil.SeedSegmentsBulk(t, pool, file.ID, "fr", 0, n, "Texte")
		f.exec(t, `UPDATE files SET segment_count = $1 WHERE id = $2`, n, file.ID)
	}

	for _, call := range []string{
		`CALL job_translation_stats_correct_all(1, 1000)`,
		`CALL job_translation_stats_correct_all(100, 1)`,
		`CALL job_translation_stats_correct_all(100, 4)`,
	} {
		f.exec(t, `TRUNCATE job_translation_stats`)
		f.exec(t, call)
		f.check(t, call)
	}
}

// TestIntegrationJobTranslationStats_RepairAllSplitsALargeFileByLanguage pins
// that the backfill recounts a file over its batch size one language per
// transaction: while a step change holds one language, the file's other
// languages are already counted, instead of the whole file waiting.
func TestIntegrationJobTranslationStats_RepairAllSplitsALargeFileByLanguage(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "SplitCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Split", "en", []string{"de", "fr", "it"})
	f := statsFixture{pool: pool, q: q, tenantID: tenant.ID, projectID: project.ID}
	// A small file before it, so the large one does not open a batch by
	// itself.
	small := testutil.SeedFile(t, q, project.ID, "small.txt", db.FileFormatTxt)
	testutil.SeedJob(t, q, small.ID, "de")
	testutil.SeedSegmentsBulk(t, pool, small.ID, "de", 0, 1, "Text")
	f.exec(t, `UPDATE files SET segment_count = 1 WHERE id = $1`, small.ID)
	file := testutil.SeedFile(t, q, project.ID, "large.txt", db.FileFormatTxt)
	for _, lang := range []string{"de", "fr", "it"} {
		testutil.SeedJob(t, q, file.ID, lang)
	}
	testutil.SeedSegmentsBulk(t, pool, file.ID, "de", 0, 20, "Text")
	f.exec(t, `INSERT INTO translations (segment_id, target_language, status, active_text)
		SELECT s.id, l, 'needs_review', 'Texte' FROM segments s, unnest(ARRAY['fr', 'it']) l WHERE s.file_id = $1`, file.ID)
	f.exec(t, `UPDATE files SET segment_count = 20 WHERE id = $1`, file.ID)
	f.exec(t, `TRUNCATE job_translation_stats`)

	// A step change on the Italian job holds its recount locks.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := holder.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	}()
	if _, err := holder.Exec(ctx, `SELECT job_translation_stats_correct(ARRAY[$1::bigint], ARRAY['it'], true)`, file.ID); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `CALL job_translation_stats_correct_all(100, 10)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("backfill with the Italian job held: %v, want a lock timeout (55P03) on Italian", err)
	}
	var counted int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(segments), 0) FROM job_translation_stats
		WHERE file_id = $1 AND target_language IN ('de', 'fr')`, file.ID).Scan(&counted); err != nil {
		t.Fatal(err)
	}
	if counted != 40 {
		t.Errorf("while Italian was held the backfill counted %d German and French segments, want all 40", counted)
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `CALL job_translation_stats_correct_all(100, 10)`)
	f.check(t, "split backfill")
}
