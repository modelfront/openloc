package taskedit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// fixture is a project with one file, one German job on a two-step workflow
// (Translate → Review) and approved translations.
type fixture struct {
	router    http.Handler
	pool      *pgxpool.Pool
	q         db.Querier
	ownerID   int64
	linguist  int64
	tenantID  int64
	projectID int64
	fileID    int64
	jobID     int64
	steps     []int64
	trs       []db.Translation
}

func newFixture(t *testing.T, prefix string, segments int, translate, review string) fixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	ownerID, tenantID := testutil.RegisterUser(t, q, prefix)
	ling := testutil.SeedUserWithRole(t, q, tenantID, prefix+"-ling@test.com", "pw", "linguist")
	proj := testutil.SeedProject(t, q, tenantID, prefix+" project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, proj.ID, prefix+".txt", db.FileFormatTxt)
	trs := make([]db.Translation, segments)
	for i := range trs {
		seg := testutil.SeedSegment(t, q, file.ID, fmt.Sprintf("Hello %d", i), int32(i+1))
		// The TXT parser's key, which the re-import XLIFF matches by.
		if _, err := pool.Exec(ctx, `UPDATE segments SET segment_key = $2 WHERE id = $1`, seg.ID, fmt.Sprintf("line:%d", i+1)); err != nil {
			t.Fatalf("seed segment key: %v", err)
		}
		trs[i] = testutil.SeedTranslation(t, q, seg.ID, "de", db.TranslationStatusApproved)
		if _, err := pool.Exec(ctx, `UPDATE translations SET active_text = $2 WHERE id = $1`, trs[i].ID, fmt.Sprintf("Hallo %d", i)); err != nil {
			t.Fatalf("seed translation text: %v", err)
		}
	}
	job := testutil.SeedJob(t, q, file.ID, "de")
	steps := testutil.SeedWorkflow(t, q, proj.SettingsProfileID, job.ID,
		testutil.WorkflowStepSpec{Name: "Translate", Assignee: &ownerID, Status: translate},
		testutil.WorkflowStepSpec{Name: "Review", Assignee: &ling.ID, Status: review},
	)
	if review == "completed" {
		if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: job.ID, Status: "completed", CurrentStep: "feedback sent"}); err != nil {
			t.Fatalf("complete job: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE projects SET status = 'completed' WHERE id = $1`, proj.ID); err != nil {
			t.Fatalf("complete project: %v", err)
		}
	}
	return fixture{
		router: testutil.NewTestRouter(pool), pool: pool, q: q,
		ownerID: ownerID, linguist: ling.ID, tenantID: tenantID,
		projectID: proj.ID, fileID: file.ID, jobID: job.ID, steps: steps, trs: trs,
	}
}

func (f fixture) draft(t *testing.T, tr db.Translation, text string, userID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoRequestWithRole(t, f.router, "PUT",
		fmt.Sprintf("/api/files/%d/translations/%d/draft", f.fileID, tr.ID),
		map[string]any{"human_text": text, "editing_time_ms": 0}, userID, f.tenantID, role)
}

func taskReopened(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	var body struct {
		TaskReopened bool `json:"task_reopened"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	return body.TaskReopened
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %s: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func (f fixture) statuses(t *testing.T) []string {
	t.Helper()
	rows, err := f.q.ListJobStepAssignments(context.Background(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Status
	}
	return out
}

func (f fixture) events(t *testing.T, eventType string) []db.ListProjectEventsRow {
	t.Helper()
	all, err := f.q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{
		ProjectID: f.projectID, TenantID: f.tenantID, Limit: 100, EventPrefix: eventType,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []db.ListProjectEventsRow
	for _, ev := range all {
		if ev.EventType == eventType {
			out = append(out, ev)
		}
	}
	return out
}

func (f fixture) activeText(t *testing.T, tr db.Translation) string {
	t.Helper()
	got, err := f.q.GetTranslation(context.Background(), tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveText == nil {
		return ""
	}
	return *got.ActiveText
}

// A manager's edit on a completed task reopens it: the task, the job and the
// project go back in progress, one history row records it, and only the first
// edit reports the reopen.
func TestIntegrationReopenByEdit_Manager(t *testing.T) {
	f := newFixture(t, "reopen-mgr", 2, "completed", "completed")
	ctx := context.Background()

	rec := f.draft(t, f.trs[0], "Hallo neu", f.ownerID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if !taskReopened(t, rec) {
		t.Fatalf("first edit must report task_reopened; body=%s", rec.Body.String())
	}
	if got := f.statuses(t); got[0] != "completed" || got[1] != "in_progress" {
		t.Fatalf("statuses = %v, want the last task (Review) reopened", got)
	}
	a, err := f.q.GetCurrentStepForJob(ctx, f.steps[1])
	if err != nil {
		t.Fatal(err)
	}
	if a.CompletedAt.Valid {
		t.Errorf("a reopened task must not keep completed_at")
	}
	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.CurrentStepID == nil || *job.CurrentStepID != f.steps[1] {
		t.Errorf("job.current_step_id = %v, want the reopened task %d", job.CurrentStepID, f.steps[1])
	}
	if job.Status == "completed" || pipeline.IsPipelineRunning(job.Status, job.CurrentStep) {
		t.Errorf("reopened job must be open and idle, got status=%q step=%q", job.Status, job.CurrentStep)
	}
	proj, err := f.q.GetProject(ctx, db.GetProjectParams{ID: f.projectID, TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}
	if proj.Status != db.ProjectStatusInProgress {
		t.Errorf("project status = %q, want in_progress after its last job reopened", proj.Status)
	}
	if n := len(f.events(t, "project.auto_reopened")); n != 1 {
		t.Errorf("project.auto_reopened events = %d, want 1", n)
	}

	reopened := f.events(t, "workflow.step_reopened")
	if len(reopened) != 1 {
		t.Fatalf("workflow.step_reopened events = %d, want 1", len(reopened))
	}
	if reopened[0].ActorID == nil || *reopened[0].ActorID != f.ownerID {
		t.Errorf("reopen actor = %v, want the editing manager %d", reopened[0].ActorID, f.ownerID)
	}
	var details map[string]any
	if err := json.Unmarshal(reopened[0].Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["step"] != "Review" || details["job_reopened"] != true || details["target_language"] != "de" {
		t.Errorf("reopen details = %v", details)
	}

	// The task is in progress now: a second edit is an ordinary edit.
	rec = f.draft(t, f.trs[1], "Hallo zwei", f.ownerID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if taskReopened(t, rec) {
		t.Errorf("only the edit that reopened the task reports it")
	}
	if n := len(f.events(t, "workflow.step_reopened")); n != 1 {
		t.Errorf("workflow.step_reopened events = %d after a second edit, want 1", n)
	}
}

// A linguist cannot reopen a completed task, even one assigned to them: the
// edit is refused and nothing moves.
func TestIntegrationReopenByEdit_LinguistRefused(t *testing.T) {
	f := newFixture(t, "reopen-ling", 1, "completed", "completed")

	rec := f.draft(t, f.trs[0], "Hallo neu", f.linguist, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if code := errorCode(t, rec); code != "task_completed" {
		t.Errorf("code = %q, want task_completed", code)
	}
	if got := f.statuses(t); got[0] != "completed" || got[1] != "completed" {
		t.Errorf("statuses = %v, a refused edit must not reopen anything", got)
	}
	if got := f.activeText(t, f.trs[0]); got == "Hallo neu" {
		t.Errorf("a refused edit must not be written")
	}
	if n := len(f.events(t, "workflow.step_reopened")); n != 0 {
		t.Errorf("workflow.step_reopened events = %d, want 0", n)
	}
}

// Concurrent first edits on a completed task reopen it exactly once.
func TestIntegrationReopenByEdit_ConcurrentEditsReopenOnce(t *testing.T) {
	const n = 8
	f := newFixture(t, "reopen-race", n, "completed", "completed")

	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = f.draft(t, f.trs[i], fmt.Sprintf("Hallo %d neu", i), f.ownerID, "owner")
		}(i)
	}
	wg.Wait()

	reported := 0
	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Fatalf("draft %d: status %d body=%s", i, rec.Code, rec.Body.String())
		}
		if taskReopened(t, rec) {
			reported++
		}
	}
	if reported != 1 {
		t.Errorf("%d responses reported the reopen, want exactly 1", reported)
	}
	if got := len(f.events(t, "workflow.step_reopened")); got != 1 {
		t.Errorf("workflow.step_reopened events = %d, want 1", got)
	}
	inProgress := 0
	for _, s := range f.statuses(t) {
		if s == "in_progress" {
			inProgress++
		}
	}
	if inProgress != 1 {
		t.Errorf("%d tasks in progress, want 1", inProgress)
	}
}

// After a reopen the task completes the normal way, and the job and project
// complete again.
func TestIntegrationReopenByEdit_CompletesAgain(t *testing.T) {
	f := newFixture(t, "reopen-cycle", 1, "completed", "completed")
	ctx := context.Background()

	testutil.AssertStatus(t, f.draft(t, f.trs[0], "Hallo neu", f.ownerID, "owner"), http.StatusOK)

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/workflow/advance", f.jobID), nil, f.ownerID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var adv struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &adv)
	if adv.Data.Status != "workflow_completed" {
		t.Fatalf("advance status = %q, want workflow_completed", adv.Data.Status)
	}
	rec = testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/complete", f.jobID), nil, f.ownerID, f.tenantID)
	testutil.AssertStatus(t, rec, http.StatusOK)

	job, err := f.q.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "completed" {
		t.Errorf("job status = %q, want completed again", job.Status)
	}
	proj, err := f.q.GetProject(ctx, db.GetProjectParams{ID: f.projectID, TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}
	if proj.Status != db.ProjectStatusCompleted {
		t.Errorf("project status = %q, want completed again", proj.Status)
	}
}

// A job whose tasks have not started takes no edits.
func TestIntegrationTaskEdit_NotStarted(t *testing.T) {
	f := newFixture(t, "not-started", 1, "pending", "pending")

	rec := f.draft(t, f.trs[0], "Hallo neu", f.ownerID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "task_not_started" {
		t.Errorf("code = %q, want task_not_started", code)
	}
}

const reimportXLIFF = `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
<file original="f.txt" source-language="en" target-language="de" datatype="plaintext"><body>
<trans-unit id="line:1"><source>Hello 0</source><target>Hallo reimport</target></trans-unit>
</body></file></xliff>`

// An errored task takes no edits from anyone, on any path that writes text.
// Apply TM/MT is the exception for a pre-translation error: running it again
// is how that error is fixed.
func TestIntegrationTaskEdit_ErroredTaskRefusedEverywhere(t *testing.T) {
	f := newFixture(t, "errored", 1, "in_progress", "pending")
	ctx := context.Background()
	if err := f.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: f.jobID, Status: "in_progress", CurrentStep: "Running " + pipeline.AIStepMarker}); err != nil {
		t.Fatal(err)
	}
	msg := "provider 503"
	if err := f.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: f.jobID, ErrorMessage: &msg}); err != nil {
		t.Fatal(err)
	}
	tr := f.trs[0]
	before := f.activeText(t, tr)

	cases := []struct {
		name, method, path string
		body               any
		code               string // "" when the refusal is not a guard code
	}{
		{"draft", "PUT", fmt.Sprintf("/api/files/%d/translations/%d/draft", f.fileID, tr.ID),
			map[string]any{"human_text": "x"}, "task_errored"},
		{"confirm", "PUT", fmt.Sprintf("/api/files/%d/translations/%d", f.fileID, tr.ID),
			map[string]any{"human_text": "x", "editing_time_ms": 0}, "task_errored"},
		{"batch draft", "PUT", fmt.Sprintf("/api/files/%d/translations/batch-draft", f.fileID),
			map[string]any{"fixes": []map[string]any{{"translation_id": tr.ID, "human_text": "x"}}}, "task_errored"},
		{"clear segment", "POST", fmt.Sprintf("/api/files/%d/translations/%d/clear", f.fileID, tr.ID),
			nil, "task_errored"},
		{"propagate without exclude_id", "POST", fmt.Sprintf("/api/files/%d/propagate", f.fileID),
			map[string]any{"source_text": "Hello 0", "target_language": "de", "human_text": "x", "exclude_id": 0}, "task_errored"},
		{"find and replace", "POST", fmt.Sprintf("/api/jobs/%d/find-replace", f.jobID),
			map[string]any{"find": "Hallo", "replace": "x", "mode": "replace_all"}, "task_errored"},
		{"apply MT after an AI-step error", "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", f.jobID),
			nil, "task_errored"},
		{"clear job", "POST", fmt.Sprintf("/api/jobs/%d/clear-translations", f.jobID),
			map[string]any{"scope": "all"}, "task_errored"},
		{"clear project", "POST", fmt.Sprintf("/api/projects/%d/clear-translations", f.projectID),
			map[string]any{"scope": "all"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, f.router, c.method, c.path, c.body, f.ownerID, f.tenantID)
			testutil.AssertStatus(t, rec, http.StatusConflict)
			if c.code != "" {
				if code := errorCode(t, rec); code != c.code {
					t.Errorf("code = %q, want %q", code, c.code)
				}
			}
			if got := f.activeText(t, tr); got != before {
				t.Errorf("refused write changed the text: %q → %q", before, got)
			}
		})
	}

	t.Run("XLIFF re-import", func(t *testing.T) {
		rec := testutil.DoFileUploadWithRole(t, f.router, fmt.Sprintf("/api/files/%d/reimport-xliff?lang=de", f.fileID),
			"edited.xliff", reimportXLIFF, f.ownerID, f.tenantID, "owner")
		testutil.AssertStatus(t, rec, http.StatusConflict)
		if code := errorCode(t, rec); code != "task_errored" {
			t.Errorf("code = %q, want task_errored", code)
		}
		if got := f.activeText(t, tr); got != before {
			t.Errorf("refused re-import changed the text: %q → %q", before, got)
		}
	})
}

// Apply TM/MT may run on a task whose pre-translation failed — that is the
// fix — while an ordinary edit still may not.
func TestIntegrationTaskEdit_PretranslationErrorAllowsApply(t *testing.T) {
	f := newFixture(t, "pretrans-err", 1, "in_progress", "pending")
	ctx := context.Background()
	if err := f.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: f.jobID, Status: "in_progress", CurrentStep: "MT: 3 / 9 segments"}); err != nil {
		t.Fatal(err)
	}
	msg := "MT provider 500"
	if err := f.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: f.jobID, ErrorMessage: &msg}); err != nil {
		t.Fatal(err)
	}

	rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", f.jobID), nil, f.ownerID, f.tenantID)
	if rec.Code == http.StatusConflict {
		t.Fatalf("Apply MT on a pre-translation error must get past the guard; got 409 %s", rec.Body.String())
	}

	rec = f.draft(t, f.trs[0], "Hallo neu", f.ownerID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)
	if code := errorCode(t, rec); code != "task_errored" {
		t.Errorf("draft code = %q, want task_errored", code)
	}
}

// Project managers reach the task matrix too, so they may reopen as well.
func TestIntegrationReopenByEdit_ProjectManager(t *testing.T) {
	f := newFixture(t, "reopen-pm", 1, "completed", "completed")
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantID, "reopen-pm-user@test.com", "pw", "project_manager")

	rec := f.draft(t, f.trs[0], "Hallo neu", pm.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if !taskReopened(t, rec) {
		t.Errorf("a project manager's edit must reopen the completed task; body=%s", rec.Body.String())
	}
}

// Apply TM/MT writes into the current task like any edit, and only in the
// first step: past it, or on a completed task the caller may not reopen, the
// server refuses before anything runs.
func TestIntegrationApplyPretranslation_FollowsTheCurrentTask(t *testing.T) {
	t.Run("current task past the first step", func(t *testing.T) {
		f := newFixture(t, "apply-second", 1, "completed", "in_progress")
		rec := testutil.DoRequest(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", f.jobID), nil, f.ownerID, f.tenantID)
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})
	t.Run("completed task, linguist", func(t *testing.T) {
		f := newFixture(t, "apply-done", 1, "completed", "completed")
		rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/jobs/%d/mt-apply", f.jobID), nil, f.linguist, f.tenantID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		if code := errorCode(t, rec); code != "task_completed" {
			t.Errorf("code = %q, want task_completed", code)
		}
		if got := f.statuses(t); got[1] != "completed" {
			t.Errorf("statuses = %v, a refused apply must not reopen", got)
		}
	})
}
