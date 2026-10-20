package projectdelete_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/projectdelete"
	"openloc/internal/testutil"
)

// taskRecorder is the router's enqueuer, recording the long tasks it is
// handed and their options.
type taskRecorder struct {
	testutil.RecordingEnqueuer
	mu    sync.Mutex
	tasks []*asynq.Task
	opts  [][]asynq.Option
}

func (r *taskRecorder) EnqueueLongTask(task *asynq.Task, opts ...asynq.Option) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	r.opts = append(r.opts, opts)
	return nil
}

func (r *taskRecorder) purges(t *testing.T) []map[string]int64 {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]int64
	for _, task := range r.tasks {
		if task.Type() != projectdelete.TaskPurge {
			continue
		}
		var p map[string]int64
		if err := json.Unmarshal(task.Payload(), &p); err != nil {
			t.Fatalf("purge payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// seeded is a project with one file, one job per target language and
// segments segments, each translated into every target language.
type seeded struct {
	project       db.Project
	file          db.File
	jobs          []db.Job
	translationID int64
}

func seedProject(t *testing.T, pool *pgxpool.Pool, q db.Querier, tenantID int64, name string, segments int, targets ...string) seeded {
	t.Helper()
	ctx := context.Background()
	project := testutil.SeedProject(t, q, tenantID, name, "en", targets)
	file := testutil.SeedFile(t, q, project.ID, name+".txt", db.FileFormatTxt)
	trIDs := testutil.SeedSegmentsBulk(t, pool, file.ID, targets[0], 0, segments, "Ziel")
	for _, lang := range targets[1:] {
		if _, err := pool.Exec(ctx, `
			INSERT INTO translations (segment_id, target_language, status)
			SELECT id, $2, 'needs_review' FROM segments WHERE file_id = $1`, file.ID, lang); err != nil {
			t.Fatalf("seed %s translations: %v", lang, err)
		}
	}
	s := seeded{project: project, file: file}
	for _, lang := range targets {
		s.jobs = append(s.jobs, testutil.SeedJob(t, q, file.ID, lang))
	}
	s.translationID = trIDs[0]
	return s
}

// projectRows counts the project row, its settings profile and every
// translation of the project.
func projectRows(t *testing.T, pool *pgxpool.Pool, s seeded) int64 {
	t.Helper()
	return testutil.CountRows(t, pool, `
		SELECT (SELECT count(*) FROM projects WHERE id = $1)
		     + (SELECT count(*) FROM settings_profiles WHERE id = $2)
		     + (SELECT count(*) FROM files WHERE project_id = $1)
		     + (SELECT count(*) FROM translations t JOIN segments s ON s.id = t.segment_id
		          JOIN files f ON f.id = s.file_id WHERE f.project_id = $1)`,
		s.project.ID, s.project.SettingsProfileID)
}

func markDeleted(t *testing.T, q db.Querier, tenantID, projectID int64) {
	t.Helper()
	n, err := q.MarkProjectDeleted(context.Background(), db.MarkProjectDeletedParams{ID: projectID, TenantID: tenantID})
	if err != nil || n != 1 {
		t.Fatalf("mark project deleted: rows %d, err %v", n, err)
	}
}

// The delete request purges nothing itself: the project is hidden from every
// read at once, and a background task is queued to purge it.
func TestIntegrationProjectDelete_RequestHidesTheProjectAndQueuesThePurge(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	rec := &taskRecorder{}
	router := testutil.NewTestRouterWithEnqueuer(pool, rec)
	userID, tenantID := testutil.RegisterUser(t, q, "hiding")
	s := seedProject(t, pool, q, tenantID, "Hidden", 20, "de")
	other := seedProject(t, pool, q, tenantID, "Kept", 5, "de")
	before := projectRows(t, pool, s)

	resp := testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/projects/%d", s.project.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	if got := projectRows(t, pool, s); got != before {
		t.Errorf("rows after the request = %d, want %d: the request must not purge", got, before)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1 AND deleted_at IS NOT NULL`, s.project.ID); n != 1 {
		t.Errorf("project not marked deleted")
	}
	purges := rec.purges(t)
	if len(purges) != 1 || purges[0]["project_id"] != s.project.ID || purges[0]["tenant_id"] != tenantID {
		t.Fatalf("queued purges = %v, want one for project %d of tenant %d", purges, s.project.ID, tenantID)
	}

	gone := []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/projects/%d", s.project.ID)},
		{"DELETE", fmt.Sprintf("/api/projects/%d", s.project.ID)},
		{"GET", fmt.Sprintf("/api/projects/%d/files/%d", s.project.ID, s.file.ID)},
		{"GET", fmt.Sprintf("/api/jobs/%d", s.jobs[0].ID)},
		{"GET", fmt.Sprintf("/api/jobs/%d/translations", s.jobs[0].ID)},
		{"PUT", fmt.Sprintf("/api/jobs/%d/holds/%d", s.jobs[0].ID, s.translationID)},
	}
	for _, g := range gone {
		resp := testutil.DoRequest(t, router, g.method, g.path, map[string]any{"tab_id": "tab-1"}, userID, tenantID)
		testutil.AssertStatus(t, resp, http.StatusNotFound)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM translation_holds WHERE job_id = $1`, s.jobs[0].ID); n != 0 {
		t.Errorf("a hold was taken on a deleted project")
	}

	resp = testutil.DoRequest(t, router, "GET", "/api/projects", nil, userID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, resp, &list)
	if len(list.Data) != 1 || list.Data[0].ID != other.project.ID {
		t.Errorf("project list = %+v, want only project %d", list.Data, other.project.ID)
	}
}

// Without a worker queue the purge runs in the request.
func TestIntegrationProjectDelete_WithoutAQueuePurgesInTheRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	userID, tenantID := testutil.RegisterUser(t, q, "inline")
	s := seedProject(t, pool, q, tenantID, "Inline", 10, "de")

	resp := testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/projects/%d", s.project.ID), nil, userID, tenantID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	if got := projectRows(t, pool, s); got != 0 {
		t.Errorf("rows left = %d, want 0", got)
	}
}

// Another organization can neither delete the project nor purge it.
func TestIntegrationProjectDelete_TenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	rec := &taskRecorder{}
	router := testutil.NewTestRouterWithEnqueuer(pool, rec)
	_, tenantA := testutil.RegisterUser(t, q, "victim")
	userB, tenantB := testutil.RegisterUser(t, q, "intruder")
	s := seedProject(t, pool, q, tenantA, "Victim", 10, "de")
	before := projectRows(t, pool, s)

	resp := testutil.DoRequest(t, router, "DELETE", fmt.Sprintf("/api/projects/%d", s.project.ID), nil, userB, tenantB)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1 AND deleted_at IS NULL`, s.project.ID); n != 1 {
		t.Errorf("another tenant's request marked the project deleted")
	}
	if len(rec.purges(t)) != 0 {
		t.Errorf("another tenant's request queued a purge")
	}

	// A purge task naming the wrong tenant leaves even a deleted project alone.
	markDeleted(t, q, tenantA, s.project.ID)
	if err := projectdelete.New(pool, nil).Purge(context.Background(), tenantB, s.project.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := projectRows(t, pool, s); got != before {
		t.Errorf("rows after a wrong-tenant purge = %d, want %d", got, before)
	}
}

// A live project is never purged, whoever asks.
func TestIntegrationProjectPurge_LeavesALiveProjectAlone(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_, tenantID := testutil.RegisterUser(t, q, "live")
	s := seedProject(t, pool, q, tenantID, "Live", 10, "de")
	before := projectRows(t, pool, s)

	if err := projectdelete.New(pool, nil).Purge(context.Background(), tenantID, s.project.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := projectRows(t, pool, s); got != before {
		t.Errorf("rows after purging a live project = %d, want %d", got, before)
	}
}

// A purge that fails part-way leaves the project hidden; the next attempt
// continues from what is left, and one after that finds nothing to do.
func TestIntegrationProjectPurge_ResumesAfterAFailureAndRepeatsHarmlessly(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "resume")
	s := seedProject(t, pool, q, tenantID, "Resume", 30, "de", "fr")
	kept := seedProject(t, pool, q, tenantID, "Neighbour", 5, "de")
	keptRows := projectRows(t, pool, kept)
	markDeleted(t, q, tenantID, s.project.ID)

	for _, stmt := range []string{
		`CREATE FUNCTION test_fail_file_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'file delete fails'; END $$`,
		`CREATE TRIGGER test_fail_file_delete BEFORE DELETE ON files FOR EACH ROW EXECUTE FUNCTION test_fail_file_delete()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the failure: %v", err)
		}
	}
	removeFailure := func() {
		for _, stmt := range []string{
			`DROP TRIGGER IF EXISTS test_fail_file_delete ON files`,
			`DROP FUNCTION IF EXISTS test_fail_file_delete()`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Logf("remove the failure: %v", err)
			}
		}
	}
	defer removeFailure()

	purger := projectdelete.New(pool, nil)
	if err := purger.Purge(ctx, tenantID, s.project.ID); err == nil {
		t.Fatal("purge succeeded with the file delete failing")
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM segments WHERE file_id = $1`, s.file.ID); n != 0 {
		t.Errorf("segments left after the first attempt = %d, want 0: the batches before the failure commit", n)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1 AND deleted_at IS NOT NULL`, s.project.ID); n != 1 {
		t.Fatalf("the project row went with the purge unfinished")
	}

	removeFailure()
	if err := purger.Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if got := projectRows(t, pool, s); got != 0 {
		t.Errorf("rows left after the second attempt = %d, want 0", got)
	}
	if err := purger.Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("third purge: %v", err)
	}
	if got := projectRows(t, pool, kept); got != keptRows {
		t.Errorf("the neighbour project's rows = %d, want %d", got, keptRows)
	}
}

// A large project goes in bounded statements: the segments in batches of 500,
// never all at once, and every row is gone at the end.
func TestIntegrationProjectPurge_LargeProjectGoesInBatches(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "large")
	s := seedProject(t, pool, q, tenantID, "Large", 1200, "de", "fr", "it")
	testutil.SeedHold(t, q, tenantID, s.jobs[0].ID, s.translationID, testutil.SeedUser(t, q, tenantID, "editor@test.com", "pass").ID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_events (project_id, tenant_id, event_type)
		SELECT $1, $2, 'test' FROM generate_series(1, 6000)`, s.project.ID, tenantID); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	markDeleted(t, q, tenantID, s.project.ID)

	// A statement-level trigger records how many segments each DELETE
	// statement removed; the table keeps them as each batch commits.
	for _, stmt := range []string{
		`CREATE TABLE test_segment_deletes (n bigint)`,
		`CREATE FUNCTION test_segment_deletes() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO test_segment_deletes SELECT count(*) FROM old_rows HAVING count(*) > 0;
			RETURN NULL;
		END $$`,
		`CREATE TRIGGER test_segment_deletes AFTER DELETE ON segments REFERENCING OLD TABLE AS old_rows
		 FOR EACH STATEMENT EXECUTE FUNCTION test_segment_deletes()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the counter: %v", err)
		}
	}
	defer func() {
		for _, stmt := range []string{
			`DROP TRIGGER IF EXISTS test_segment_deletes ON segments`,
			`DROP FUNCTION IF EXISTS test_segment_deletes()`,
			`DROP TABLE IF EXISTS test_segment_deletes`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Logf("remove the counter: %v", err)
			}
		}
	}()

	if err := projectdelete.New(pool, nil).Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := projectRows(t, pool, s); got != 0 {
		t.Errorf("rows left = %d, want 0", got)
	}
	for table, query := range map[string]string{
		"project events": `SELECT count(*) FROM project_events WHERE project_id = $1`,
		"jobs":           `SELECT count(*) FROM jobs j JOIN files f ON f.id = j.file_id WHERE f.project_id = $1`,
	} {
		if n := testutil.CountRows(t, pool, query, s.project.ID); n != 0 {
			t.Errorf("%s left = %d, want 0", table, n)
		}
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM translation_holds WHERE job_id = $1`, s.jobs[0].ID); n != 0 {
		t.Errorf("holds left = %d, want 0", n)
	}
	// 1200 segments in batches of 500: three statements, none larger.
	var statements, largest int64
	if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(max(n), 0) FROM test_segment_deletes`).Scan(&statements, &largest); err != nil {
		t.Fatalf("read the counter: %v", err)
	}
	if statements != 3 || largest != 500 {
		t.Errorf("segment DELETE statements = %d, largest %d; want 3, largest 500", statements, largest)
	}
}

// While another purge of the project holds its lock, a second one returns at
// once and touches nothing.
func TestIntegrationProjectPurge_SecondPurgeStopsWhileOneRuns(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "locked")
	s := seedProject(t, pool, q, tenantID, "Locked", 10, "de")
	markDeleted(t, q, tenantID, s.project.ID)
	before := projectRows(t, pool, s)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	lq := db.New(conn)
	if locked, err := lq.TryLockProjectDelete(ctx, s.project.ID); err != nil || !locked {
		t.Fatalf("take the lock: locked %v, err %v", locked, err)
	}
	if err := projectdelete.New(pool, nil).Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := projectRows(t, pool, s); got != before {
		t.Errorf("rows after a locked-out purge = %d, want %d", got, before)
	}
	if _, err := lq.UnlockProjectDelete(ctx, s.project.ID); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

// The sweep queues a purge for every project still marked deleted, and none
// for a live one.
func TestIntegrationProjectPurgeSweep_QueuesEveryDeletedProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	_, tenantA := testutil.RegisterUser(t, q, "sweepa")
	_, tenantB := testutil.RegisterUser(t, q, "sweepb")
	a := seedProject(t, pool, q, tenantA, "A", 1, "de")
	b := seedProject(t, pool, q, tenantB, "B", 1, "de")
	seedProject(t, pool, q, tenantA, "Live", 1, "de")
	markDeleted(t, q, tenantA, a.project.ID)
	markDeleted(t, q, tenantB, b.project.ID)

	rec := &taskRecorder{}
	if err := projectdelete.New(pool, rec).HandleSweep(context.Background(), projectdelete.NewSweepTask()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := map[int64]int64{}
	for _, p := range rec.purges(t) {
		got[p["project_id"]] = p["tenant_id"]
	}
	want := map[int64]int64{a.project.ID: tenantA, b.project.ID: tenantB}
	if len(got) != len(want) || got[a.project.ID] != tenantA || got[b.project.ID] != tenantB {
		t.Errorf("queued purges = %v, want %v", got, want)
	}
}

// The draft-only mark refuses a project published since the caller read it.
func TestIntegrationMarkProjectDeleted_DraftOnlyRefusesALiveProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "draftonly")
	s := seedProject(t, pool, q, tenantID, "Live", 1, "de")

	n, err := q.MarkProjectDeleted(ctx, db.MarkProjectDeletedParams{ID: s.project.ID, TenantID: tenantID, DraftOnly: true})
	if err != nil || n != 0 {
		t.Fatalf("draft-only mark of a live project: rows %d, err %v; want 0", n, err)
	}
	if c := testutil.CountRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1 AND deleted_at IS NULL`, s.project.ID); c != 1 {
		t.Errorf("the live project was marked deleted")
	}
}

// A deleted project's settings no longer count as a use of any shared
// resource, so none of them is blocked or shown as in use while the purge is
// pending.
func TestIntegrationProjectDelete_UsageCountsSkipADeletedProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "usage")
	s := seedProject(t, pool, q, tenantID, "User", 1, "de")
	profileID := s.project.SettingsProfileID

	tm := testutil.SeedTM(t, q, tenantID, "Shared TM")
	tmProfile := testutil.SeedTMProfile(t, q, tenantID, "Shared profile", 75)
	testutil.LinkTMProfile(t, q, profileID, tmProfile.ID)
	mtRouter := testutil.SeedMTRouter(t, q, tenantID, "Shared MT router", nil, nil)
	aiRouter := testutil.SeedAIRouter(t, q, tenantID, "Shared AI router", nil).ID
	termBase := testutil.SeedTermBase(t, q, tenantID, "Shared terms", "en", "de")
	template := testutil.SeedTemplate(t, q, tenantID, "Source template")
	var qaProfile, workflow, customer int64
	for _, seed := range []struct {
		dst *int64
		sql string
	}{
		{&qaProfile, `INSERT INTO qa_profiles (tenant_id, name) VALUES ($1, 'Shared QA') RETURNING id`},
		{&workflow, `INSERT INTO workflows (tenant_id, name) VALUES ($1, 'Shared workflow') RETURNING id`},
		{&customer, `INSERT INTO customers (tenant_id, name) VALUES ($1, 'Shared customer') RETURNING id`},
	} {
		if err := pool.QueryRow(ctx, seed.sql, tenantID).Scan(seed.dst); err != nil {
			t.Fatalf("seed %q: %v", seed.sql, err)
		}
	}
	for _, link := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE settings_profiles SET mt_router_id = $2, qa_profile_id = $3, workflow_id = $4 WHERE id = $1`,
			[]any{profileID, mtRouter, qaProfile, workflow}},
		{`INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, mode, priority, penalty_pct)
		  VALUES ($1, $2, 'read', 1, 0)`, []any{profileID, tm.ID}},
		{`INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, mode, priority)
		  VALUES ($1, $2, 'en', 'de', 'read', 1)`, []any{profileID, termBase.ID}},
		{`INSERT INTO workflow_steps (name, settings_profile_id, ai_router_id) VALUES ('AI review', $1, $2)`,
			[]any{profileID, aiRouter}},
		{`UPDATE projects SET template_id = $2, customer_id = $3 WHERE id = $1`,
			[]any{s.project.ID, template.ID, customer}},
	} {
		if _, err := pool.Exec(ctx, link.sql, link.args...); err != nil {
			t.Fatalf("link %q: %v", link.sql, err)
		}
	}

	counts := map[string]func() (int64, error){
		"TM": func() (int64, error) {
			n, err := q.CountTMReferences(ctx, tm.ID)
			return int64(n), err
		},
		"TM profile": func() (int64, error) {
			r, err := q.CountTMProfileUsage(ctx, db.CountTMProfileUsageParams{TmProfileID: &tmProfile.ID, TenantID: tenantID})
			return int64(r.ProjectCount), err
		},
		"TM profile list": func() (int64, error) {
			rows, err := q.ListTMProfiles(ctx, tenantID)
			for _, r := range rows {
				if r.ID == tmProfile.ID {
					return int64(r.UsageCount), err
				}
			}
			return -1, err
		},
		"TM profile detail": func() (int64, error) {
			r, err := q.GetTMProfileWithStats(ctx, db.GetTMProfileWithStatsParams{ID: tmProfile.ID, TenantID: tenantID})
			return int64(r.UsageCount), err
		},
		"MT router": func() (int64, error) {
			n, err := q.CountProfileRefsForMTRouter(ctx, db.CountProfileRefsForMTRouterParams{RouterID: &mtRouter, TenantID: tenantID})
			return int64(n), err
		},
		"AI router": func() (int64, error) {
			n, err := q.CountStepRefsForAIRouter(ctx, db.CountStepRefsForAIRouterParams{RouterID: &aiRouter, TenantID: tenantID})
			return int64(n), err
		},
		"QA profile": func() (int64, error) {
			n, err := q.CountQAProfileReferences(ctx, &qaProfile)
			return int64(n), err
		},
		"term base": func() (int64, error) {
			r, err := q.CountTermBaseUsage(ctx, db.CountTermBaseUsageParams{TermBaseID: termBase.ID, TenantID: tenantID})
			return int64(r.ProjectCount), err
		},
		"template": func() (int64, error) {
			return q.CountProjectsFromTemplate(ctx, db.CountProjectsFromTemplateParams{TemplateID: &template.ID, TenantID: tenantID})
		},
		"workflow": func() (int64, error) {
			r, err := q.CountProfilesFromWorkflow(ctx, db.CountProfilesFromWorkflowParams{WorkflowID: &workflow, TenantID: tenantID})
			return r.Projects, err
		},
		"customer": func() (int64, error) {
			return q.CountProjectsForCustomer(ctx, db.CountProjectsForCustomerParams{CustomerID: &customer, TenantID: tenantID})
		},
	}
	check := func(when string, want int64) {
		t.Helper()
		for name, count := range counts {
			n, err := count()
			if err != nil {
				t.Fatalf("%s count %s: %v", name, when, err)
			}
			if n != want {
				t.Errorf("%s count %s = %d, want %d", name, when, n, want)
			}
		}
	}
	check("before the delete", 1)
	markDeleted(t, q, tenantID, s.project.ID)
	check("after the delete", 0)
}

// A segment batch that loses a deadlock (an open editor writing a hold on the
// rows it cascades through) runs again, and the purge still finishes.
func TestIntegrationProjectPurge_RetriesABatchThatLostADeadlock(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "deadlock")
	s := seedProject(t, pool, q, tenantID, "Deadlocked", 20, "de")
	markDeleted(t, q, tenantID, s.project.ID)

	// The first DELETE statement on segments fails as Postgres fails a deadlock
	// victim (SQLSTATE 40P01); a sequence counts the statements, since nextval
	// survives the failed attempt's rollback.
	for _, stmt := range []string{
		`CREATE SEQUENCE test_segment_deadlock`,
		`CREATE FUNCTION test_segment_deadlock() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF nextval('test_segment_deadlock') = 1 THEN
				RAISE EXCEPTION 'deadlock detected' USING ERRCODE = '40P01';
			END IF;
			RETURN NULL;
		END $$`,
		`CREATE TRIGGER test_segment_deadlock BEFORE DELETE ON segments FOR EACH STATEMENT EXECUTE FUNCTION test_segment_deadlock()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the deadlock: %v", err)
		}
	}
	defer func() {
		for _, stmt := range []string{
			`DROP TRIGGER IF EXISTS test_segment_deadlock ON segments`,
			`DROP FUNCTION IF EXISTS test_segment_deadlock()`,
			`DROP SEQUENCE IF EXISTS test_segment_deadlock`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Logf("remove the deadlock: %v", err)
			}
		}
	}()

	if err := projectdelete.New(pool, nil).Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("purge after a lost deadlock: %v", err)
	}
	if got := projectRows(t, pool, s); got != 0 {
		t.Errorf("rows left = %d, want 0", got)
	}
}

// The organization-wide reads (analytics, the MCP job list, label usage, task
// list, deadline reminders) drop a deleted project at once and keep its
// neighbour.
func TestIntegrationProjectDelete_TenantWideReadsDropTheProject(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	userID, tenantID := testutil.RegisterUser(t, q, "reads")
	deleted := seedProject(t, pool, q, tenantID, "Deleted", 1, "de")
	kept := seedProject(t, pool, q, tenantID, "Kept", 1, "de")
	labels := map[int64]int64{} // project id → its customer label value id
	for _, s := range []seeded{deleted, kept} {
		testutil.SeedJobStep(t, q, s.project.SettingsProfileID, s.jobs[0].ID, &userID)
		if _, err := pool.Exec(ctx, `UPDATE job_step_assignments SET deadline = now() + interval '12 hours' WHERE job_id = $1`, s.jobs[0].ID); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		labels[s.project.ID] = testutil.SeedProjectSystemLabel(t, q, tenantID, s.project.ID, "customer", s.project.Name+" customer").ID
	}

	// reads returns, per read, whether it shows each project.
	reads := func() map[string]map[int64]bool {
		t.Helper()
		out := map[string]map[int64]bool{}
		seen := func(read string, projectID int64) {
			if out[read] == nil {
				out[read] = map[int64]bool{}
			}
			out[read][projectID] = true
		}
		jobProject := map[int64]int64{deleted.jobs[0].ID: deleted.project.ID, kept.jobs[0].ID: kept.project.ID}
		labelProject := map[int64]int64{labels[deleted.project.ID]: deleted.project.ID, labels[kept.project.ID]: kept.project.ID}

		ids, err := q.ListProgressProjectPage(ctx, db.ListProgressProjectPageParams{TenantID: tenantID, RowLimit: 10})
		if err != nil {
			t.Fatalf("progress report: %v", err)
		}
		for _, id := range ids {
			seen("progress report", id)
		}
		mcpJobs, err := q.ListMCPJobs(ctx, db.ListMCPJobsParams{TenantID: tenantID, RowLimit: 10})
		if err != nil {
			t.Fatalf("MCP jobs: %v", err)
		}
		for _, j := range mcpJobs {
			seen("MCP jobs", jobProject[j.JobID])
		}
		usage, err := q.ListLabelValueUsage(ctx, tenantID)
		if err != nil {
			t.Fatalf("label usage: %v", err)
		}
		for _, u := range usage {
			if p, ok := labelProject[u.LabelValueID]; ok && u.ProjectCount > 0 {
				seen("label usage", p)
			}
		}
		tasks, err := q.ListTasksFiltered(ctx, db.ListTasksFilteredParams{TenantID: tenantID, Lim: 10})
		if err != nil {
			t.Fatalf("task list: %v", err)
		}
		for _, task := range tasks {
			seen("task list", jobProject[task.ID])
		}
		due, err := q.GetJobsApproachingDeadline(ctx, &tenantID)
		if err != nil {
			t.Fatalf("deadline reminders: %v", err)
		}
		for _, d := range due {
			seen("deadline reminders", jobProject[d.JobID])
		}
		return out
	}

	check := func(when string, wantDeleted bool) {
		t.Helper()
		got := reads()
		for _, read := range []string{"progress report", "MCP jobs", "label usage", "task list", "deadline reminders"} {
			if !got[read][kept.project.ID] {
				t.Errorf("%s %s: the kept project is missing", read, when)
			}
			if got[read][deleted.project.ID] != wantDeleted {
				t.Errorf("%s %s: shows the deleted project = %v, want %v", read, when, got[read][deleted.project.ID], wantDeleted)
			}
		}
	}
	check("before the delete", true)
	markDeleted(t, q, tenantID, deleted.project.ID)
	check("after the delete", false)
}

// A deleted project's job counts are not recounted while its segments go
// batch by batch: the purge writes no count rows, and leaves none behind.
func TestIntegrationProjectPurge_DoesNotRecountTheJobsItDeletes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, tenantID := testutil.RegisterUser(t, q, "counts")
	s := seedProject(t, pool, q, tenantID, "Counted", 1200, "de", "fr")
	kept := seedProject(t, pool, q, tenantID, "Neighbour", 10, "de")
	statsOf := func(fileID int64) int64 {
		return testutil.CountRows(t, pool, `SELECT COALESCE(sum(segments), 0) FROM job_translation_stats WHERE file_id = $1`, fileID)
	}
	if n := statsOf(s.file.ID); n != 2400 {
		t.Fatalf("counts before the delete = %d, want 2400", n)
	}
	markDeleted(t, q, tenantID, s.project.ID)

	for _, stmt := range []string{
		`CREATE TABLE test_stats_inserts (n bigint)`,
		`CREATE FUNCTION test_stats_inserts() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO test_stats_inserts SELECT count(*) FROM new_rows HAVING count(*) > 0;
			RETURN NULL;
		END $$`,
		`CREATE TRIGGER test_stats_inserts AFTER INSERT ON job_translation_stats REFERENCING NEW TABLE AS new_rows
		 FOR EACH STATEMENT EXECUTE FUNCTION test_stats_inserts()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the counter: %v", err)
		}
	}
	defer func() {
		for _, stmt := range []string{
			`DROP TRIGGER IF EXISTS test_stats_inserts ON job_translation_stats`,
			`DROP FUNCTION IF EXISTS test_stats_inserts()`,
			`DROP TABLE IF EXISTS test_stats_inserts`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Logf("remove the counter: %v", err)
			}
		}
	}()

	if err := projectdelete.New(pool, nil).Purge(ctx, tenantID, s.project.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM test_stats_inserts`); n != 0 {
		t.Errorf("count writes during the purge = %d statements, want 0", n)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM job_translation_stats WHERE file_id = $1`, s.file.ID); n != 0 {
		t.Errorf("count rows left for the purged file = %d, want 0", n)
	}
	if n := statsOf(kept.file.ID); n != 10 {
		t.Errorf("the neighbour's counts = %d, want 10", n)
	}
}
