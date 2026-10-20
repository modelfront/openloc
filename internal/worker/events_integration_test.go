package worker_test

// Integration tests for the worker's project_events emissions:
// async pipeline outcomes land in the project history as
// SYSTEM events with the triggering user's email snapshotted into details.
// External test package: the internal worker tests use mocks; these need the
// real DB via testutil, which transitively imports internal/api → worker.
//
// Anti-tautology: removing the emitJobPipelineEvent calls in HandleTranslate
// makes both subtests fail on "expected 1 event, got 0".

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/worker"
)

// fakePipeline satisfies worker.PipelineService with a configurable outcome.
type fakePipeline struct {
	err      error
	outcome  pipeline.TranslateOutcome
	aiResult pipeline.AIStepResult
	calls    int
}

func (f *fakePipeline) TranslateFile(context.Context, int64, int64, string, string, pipeline.ProgressFunc, pipeline.TranslatePhases) (pipeline.TranslateOutcome, error) {
	f.calls++
	return f.outcome, f.err
}
func (f *fakePipeline) RunAIStep(context.Context, int64, int64, pipeline.ProgressFunc) (pipeline.AIStepResult, error) {
	f.calls++
	return f.aiResult, f.err
}

func listEventTypes(t *testing.T, q db.Querier, projectID, tenantID int64) (map[string]map[string]any, map[string]int) {
	t.Helper()
	out := map[string]map[string]any{}
	counts := map[string]int{}
	for _, r := range testutil.ProjectEvents(t, q, projectID, tenantID, "") {
		if r.ActorID != nil {
			t.Errorf("pipeline events must be system events (nil actor), got actor %d on %s", *r.ActorID, r.EventType)
		}
		out[r.EventType] = r.Data
		counts[r.EventType]++
	}
	return out, counts
}

// findEventForFile is lookupEventForFile that fatals when the event is absent.
func findEventForFile(t *testing.T, q db.Querier, projectID, tenantID int64, eventType, fileName string) map[string]any {
	t.Helper()
	found := lookupEventForFile(t, q, projectID, tenantID, eventType, fileName)
	if found == nil {
		t.Fatalf("no %s event for %s", eventType, fileName)
	}
	return found
}

// lookupEventForFile returns the details of the single eventType event whose
// file_name matches, or nil when there is none. Fatals on two matches. The
// shared project accumulates events across subtests, so type alone is
// ambiguous.
func lookupEventForFile(t *testing.T, q db.Querier, projectID, tenantID int64, eventType, fileName string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, r := range testutil.ProjectEvents(t, q, projectID, tenantID, eventType) {
		if r.Data["file_name"] == fileName {
			if found != nil {
				t.Fatalf("multiple %s events for %s", eventType, fileName)
			}
			found = r.Data
		}
	}
	return found
}

func TestIntegrationWorkerPipelineEvents(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "WorkerEventsCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "trigger@worker.test", "pass")
	testutil.SetUserName(t, q, user.ID, "Tia Trigger")
	project := testutil.SeedProject(t, q, tenant.ID, "Worker Events", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "w.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	payload := worker.PipelinePayload{
		FileID: file.ID, SourceLanguage: "en", TargetLanguage: "de",
		TenantID: tenant.ID, JobID: job.ID,
		Phases:      pipeline.TranslatePhases{UseTM: true, UseMT: true},
		TriggeredBy: &user.ID,
	}
	task := pipelineTask(t, worker.TaskTranslate, payload)

	t.Run("success emits job.pipeline_completed", func(t *testing.T) {
		h := worker.NewHandler(&fakePipeline{}, q)
		if err := h.HandleTranslate(ctx, task); err != nil {
			t.Fatalf("HandleTranslate: %v", err)
		}
		events, counts := listEventTypes(t, q, project.ID, tenant.ID)
		if counts["job.pipeline_completed"] != 1 {
			t.Fatalf("expected exactly 1 job.pipeline_completed event, got %d (%v)", counts["job.pipeline_completed"], events)
		}
		details := events["job.pipeline_completed"]
		if details["file_name"] != "w.txt" || details["target_language"] != "de" {
			t.Errorf("expected file snapshot in details, got %v", details)
		}
		if details["triggered_by_name"] != "Tia Trigger" {
			t.Errorf("expected triggered_by_name snapshot, got %v", details["triggered_by_name"])
		}
		// A noun phrase, not the "Running TM lookup + MT" progress label —
		// the History tab renders it inside "finished <phase> for <file>".
		if details["phase"] != "TM + MT pre-translation" {
			t.Errorf("expected phase noun %q, got %v", "TM + MT pre-translation", details["phase"])
		}
	})

	t.Run("skipped phases ride the completed event", func(t *testing.T) {
		// Fresh job so this run's event is unambiguous in the accumulated list.
		skipFile := testutil.SeedFile(t, q, project.ID, "skip.txt", db.FileFormatTxt)
		skipJob := testutil.SeedJob(t, q, skipFile.ID, "de")
		skipPayload := worker.PipelinePayload{
			FileID: skipFile.ID, SourceLanguage: "en", TargetLanguage: "de",
			TenantID: tenant.ID, JobID: skipJob.ID,
			Phases: pipeline.TranslatePhases{UseTM: true, UseMT: true},
		}
		h := worker.NewHandler(&fakePipeline{outcome: pipeline.TranslateOutcome{
			TMSkipReason: pipeline.PhaseSkipNoAssignment,
		}}, q)
		if err := h.HandleTranslate(ctx, pipelineTask(t, worker.TaskTranslate, skipPayload)); err != nil {
			t.Fatalf("HandleTranslate: %v", err)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_completed", "skip.txt")
		if details["tm_skipped"] != "no_assignment" {
			t.Errorf("expected tm_skipped=no_assignment in details, got %v", details)
		}
		if _, present := details["mt_skipped"]; present {
			t.Errorf("mt ran — mt_skipped must be absent, got %v", details["mt_skipped"])
		}
		// A skipped phase counts nothing, so it records no counts.
		if _, present := details["tm_exact"]; present {
			t.Errorf("tm skipped — tm_exact must be absent, got %v", details)
		}
	})

	t.Run("what each phase filled rides the completed event", func(t *testing.T) {
		countFile := testutil.SeedFile(t, q, project.ID, "counts.txt", db.FileFormatTxt)
		countJob := testutil.SeedJob(t, q, countFile.ID, "de")
		countPayload := worker.PipelinePayload{
			FileID: countFile.ID, SourceLanguage: "en", TargetLanguage: "de",
			TenantID: tenant.ID, JobID: countJob.ID,
			Phases: pipeline.TranslatePhases{UseTM: true, UseMT: true},
		}
		h := worker.NewHandler(&fakePipeline{outcome: pipeline.TranslateOutcome{
			TMExact: 12, TMFuzzy: 3, TMNames: []string{"Default TM", "Client TM"}, MTTranslated: 40, MTEngine: "DeepL Pro",
		}}, q)
		if err := h.HandleTranslate(ctx, pipelineTask(t, worker.TaskTranslate, countPayload)); err != nil {
			t.Fatalf("HandleTranslate: %v", err)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_completed", "counts.txt")
		// JSON numbers decode as float64.
		if details["tm_exact"] != float64(12) || details["tm_fuzzy"] != float64(3) ||
			details["mt_translated"] != float64(40) || details["mt_engine"] != "DeepL Pro" {
			t.Errorf("expected tm_exact=12 tm_fuzzy=3 mt_translated=40 mt_engine=DeepL Pro, got %v", details)
		}
		if names, _ := details["tm_names"].([]any); len(names) != 2 || names[0] != "Default TM" || names[1] != "Client TM" {
			t.Errorf("expected tm_names [Default TM, Client TM] in priority order, got %v", details["tm_names"])
		}
	})

	t.Run("QE+APE outcome counts ride the completed event", func(t *testing.T) {
		aiFile := testutil.SeedFile(t, q, project.ID, "ai.txt", db.FileFormatTxt)
		aiJob := testutil.SeedJob(t, q, aiFile.ID, "de")
		aiPayload := worker.PipelinePayload{
			TenantID: tenant.ID, JobID: aiJob.ID,
		}
		h := worker.NewHandler(&fakePipeline{aiResult: pipeline.AIStepResult{
			Total: 15, AutoApproved: 12, NeedsReview: 3,
		}}, q)
		if err := h.HandleAIStep(ctx, pipelineTask(t, worker.TaskAIStep, aiPayload)); err != nil {
			t.Fatalf("HandleAIStep: %v", err)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_completed", "ai.txt")
		if details["phase"] != "QE+APE" {
			t.Errorf("expected phase QE+APE, got %v", details["phase"])
		}
		// JSON numbers decode as float64.
		if details["auto_approved"] != float64(12) || details["needs_review"] != float64(3) {
			t.Errorf("expected auto_approved=12 needs_review=3, got %v", details)
		}
	})

	t.Run("stop requested while queued emits job.pipeline_stopped", func(t *testing.T) {
		stopFile := testutil.SeedFile(t, q, project.ID, "stop.txt", db.FileFormatTxt)
		stopJob := testutil.SeedJob(t, q, stopFile.ID, "de")
		// The job is queued-and-running from the API's point of view when the
		// user presses Stop; the worker then picks the task up.
		if err := q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: stopJob.ID, Status: "queued", CurrentStep: "Queued for pre-translation"}); err != nil {
			t.Fatalf("seed queued job: %v", err)
		}
		if _, err := q.RequestJobStop(ctx, stopJob.ID); err != nil {
			t.Fatalf("RequestJobStop: %v", err)
		}
		stopPayload := worker.PipelinePayload{
			FileID: stopFile.ID, SourceLanguage: "en", TargetLanguage: "de",
			TenantID: tenant.ID, JobID: stopJob.ID,
			Phases: pipeline.TranslatePhases{UseMT: true},
		}
		fake := &fakePipeline{}
		h := worker.NewHandler(fake, q)
		if err := h.HandleTranslate(ctx, pipelineTask(t, worker.TaskTranslate, stopPayload)); err != nil {
			t.Fatalf("a stopped run must not fail the task: %v", err)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_stopped", "stop.txt")
		if details["phase"] != "MT pre-translation" {
			t.Errorf("expected phase noun in details, got %v", details["phase"])
		}
		post, err := q.GetJob(ctx, stopJob.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if post.Status != "stopped" || post.StopRequestedAt.Valid {
			t.Errorf("job = status %q flag %v, want stopped with the flag cleared", post.Status, post.StopRequestedAt.Valid)
		}
	})

	t.Run("failure emits job.pipeline_failed", func(t *testing.T) {
		h := worker.NewHandler(&fakePipeline{err: fmt.Errorf("provider quota exceeded")}, q)
		if err := h.HandleTranslate(ctx, task); err == nil {
			t.Fatal("expected HandleTranslate to propagate the pipeline error")
		}
		events, counts := listEventTypes(t, q, project.ID, tenant.ID)
		if counts["job.pipeline_failed"] != 1 {
			t.Fatalf("expected exactly 1 job.pipeline_failed event, got %d", counts["job.pipeline_failed"])
		}
		if details := events["job.pipeline_failed"]; details["error"] != "provider quota exceeded" {
			t.Errorf("expected short error in details, got %v", details["error"])
		}
	})

	// The two below run after "failure emits job.pipeline_failed", which counts
	// every failed event in the shared project.
	runAttempt := func(t *testing.T, fileName string, runErr error) (db.Job, error) {
		t.Helper()
		f := testutil.SeedFile(t, q, project.ID, fileName, db.FileFormatTxt)
		j := testutil.SeedJob(t, q, f.ID, "de")
		p := worker.PipelinePayload{
			FileID: f.ID, SourceLanguage: "en", TargetLanguage: "de",
			TenantID: tenant.ID, JobID: j.ID, Phases: pipeline.TranslatePhases{UseMT: true},
		}
		defer worker.SetLastAttemptForTest(false)()
		taskErr := worker.NewHandler(&fakePipeline{err: runErr}, q).HandleTranslate(ctx, pipelineTask(t, worker.TaskTranslate, p))
		post, err := q.GetJob(ctx, j.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		return post, taskErr
	}

	t.Run("a transient failure on a non-final attempt reads as retrying", func(t *testing.T) {
		post, err := runAttempt(t, "transient.txt", fmt.Errorf("provider 503"))
		if err == nil || errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("a transient failure must return a retryable error, got %v", err)
		}
		if !pipeline.IsPipelineRunning(post.Status, post.CurrentStep) {
			t.Errorf("job = %q / %q, want it still running until asynq's last attempt", post.Status, post.CurrentStep)
		}
		if lookupEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_failed", "transient.txt") != nil {
			t.Error("a retried attempt must not record job.pipeline_failed")
		}
	})

	t.Run("a permanent failure fails the job on a non-final attempt", func(t *testing.T) {
		refused := "Machine translation failed: the libretranslate engine does not translate en → de."
		post, err := runAttempt(t, "permanent.txt", pipeline.Permanent(httputil.NewClientError(refused)))
		if !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("a permanent failure must tell asynq not to retry, got %v", err)
		}
		if post.Status != "failed" || post.ErrorMessage == nil || *post.ErrorMessage != refused {
			t.Errorf("job = %q with error %v, want failed with %q", post.Status, post.ErrorMessage, refused)
		}
		details := findEventForFile(t, q, project.ID, tenant.ID, "job.pipeline_failed", "permanent.txt")
		if details["error"] != refused {
			t.Errorf("expected the refusal in the event details, got %v", details["error"])
		}
	})
}
