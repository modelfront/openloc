package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
)

// TaskEnqueuer enqueues pipeline tasks for async processing. The `phases`
// arg controls which translate phases run (TM, MT, or both). Predict runs as
// a separate TaskAIStep that the worker chains after the pipeline when the
// job's active step carries a QE+APE engine.
//
// EnqueueAnalysis is for the saved-analyses feature. The analysis row must already exist with
// status='pending'; the worker picks it up and computes breakdowns.
type TaskEnqueuer interface {
	EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error
	EnqueueAnalysis(analysisID, tenantID int64) error
}

// asynqEnqueueClient is the slice of *asynq.Client the Enqueuer needs. Kept as
// an interface so tests can inject a stub and assert the pre-enqueue job-status
// writes (queued labels) without a live Redis.
type asynqEnqueueClient interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Enqueuer implements TaskEnqueuer using an asynq client.
type Enqueuer struct {
	client  asynqEnqueueClient
	queries db.Querier
	// queue namespaces this deployment's tasks. Each
	// deployment consumes its own queue: every non-master branch shares
	// redis-staging and the staging database, and asynq has no key prefix, so
	// a shared queue lets one branch's worker run another branch's jobs with
	// the wrong code. Empty means DefaultQueue.
	queue string
}

// NewEnqueuer creates an Enqueuer backed by the given asynq client, publishing
// to the given queue. Pass "" for asynq's default queue.
func NewEnqueuer(client *asynq.Client, queries db.Querier, queue string) *Enqueuer {
	return &Enqueuer{client: client, queries: queries, queue: QueueOrDefault(queue)}
}

// QueueOrDefault normalises a configured queue name. One helper so the enqueue
// side and the consume side cannot disagree — a producer publishing to a queue
// nobody consumes is a silent backlog.
func QueueOrDefault(q string) string {
	if q = strings.TrimSpace(q); q != "" {
		return q
	}
	return "default"
}

// EnqueuePipeline enqueues a pipeline task (translate phases) for a file/language pair.
// Phases controls which translate phases run (TM only / MT only / both).
// triggeredBy is the user who initiated the work (uploader / Pre-translate
// clicker / nil for system-initiated); the worker propagates it to emission
// sites via context.
// It immediately marks the job as "queued" so the frontend knows processing has started.
func (e *Enqueuer) EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error {
	task, err := NewPipelineTask(TaskPipeline, PipelinePayload{
		FileID:         fileID,
		SourceLanguage: sourceLang,
		TargetLanguage: targetLang,
		JobID:          jobID,
		TenantID:       tenantID,
		Phases:         phases,
		TriggeredBy:    triggeredBy,
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	// Mark job as queued immediately so the frontend can start polling
	if jobID != 0 {
		if err := e.queries.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{
			ID:          jobID,
			Status:      "queued",
			CurrentStep: "Queued for pre-translation",
		}); err != nil {
			logging.Warn(context.Background(), err, "could not mark job queued", "worker-job-queued-failed", logging.JobID(jobID))
		}
	}

	info, err := e.client.Enqueue(task,
		asynq.Queue(LongQueue(e.queue)),
		asynq.MaxRetry(3),          // 3 retries (not 25 default)
		asynq.Timeout(2*time.Hour), // generous for large files on slow local MT
	)
	if err != nil {
		return fmt.Errorf("enqueue task: %w", err)
	}

	// Store asynq task ID so we can cancel on job delete
	if jobID != 0 && info != nil {
		if err := e.queries.SetJobAsynqTaskID(context.Background(), db.SetJobAsynqTaskIDParams{
			ID:          jobID,
			AsynqTaskID: &info.ID,
		}); err != nil {
			logging.Warn(context.Background(), err, "could not store the asynq task id", "worker-asynq-id-store-failed", logging.JobID(jobID))
		}
	}

	return nil
}

// aiStepMaxRetry gives the QE+APE step 3 attempts in total.
const aiStepMaxRetry = 2

// EnqueueAIStep dispatches a TaskAIStep for a job whose active workflow step
// carries a QE+APE AI engine (predict-on-start). triggeredBy attributes
// any APE history rows.
//
// It immediately marks the job `queued` + "Queued for QE+APE" (mirrors
// EnqueuePipeline) so the window between enqueue and worker pickup is visible in
// the UI and trips the editor lock — `isPipelineRunning`/`isAIStepRunning` key
// off this status + the "QE+APE" current_step label. The worker then flips to
// "Running QE+APE" and finally "QE+APE complete". These labels must stay in sync
// with the frontend matcher in web/src/lib/project-logic.ts (isAIStepRunning).
func (e *Enqueuer) EnqueueAIStep(jobID, tenantID int64, triggeredBy *int64) error {
	task, err := NewPipelineTask(TaskAIStep, PipelinePayload{
		JobID:       jobID,
		TenantID:    tenantID,
		TriggeredBy: triggeredBy,
	})
	if err != nil {
		return fmt.Errorf("create ai-step task: %w", err)
	}

	if jobID != 0 {
		if err := e.queries.UpdateJobStatus(context.Background(), db.UpdateJobStatusParams{
			ID:          jobID,
			Status:      "queued",
			CurrentStep: "Queued for " + pipeline.AIStepMarker,
		}); err != nil {
			logging.Warn(context.Background(), err, "could not mark job queued for QE+APE", "worker-job-queued-ai-failed", logging.JobID(jobID))
		}
	}

	if _, err := e.client.Enqueue(task,
		asynq.Queue(LongQueue(e.queue)),
		asynq.MaxRetry(aiStepMaxRetry),
		asynq.Timeout(30*time.Minute),
	); err != nil {
		return fmt.Errorf("enqueue ai-step task: %w", err)
	}
	return nil
}

// EnqueueTask dispatches a task another package built, on this deployment's
// queue, so its producer never picks the queue itself.
func (e *Enqueuer) EnqueueTask(task *asynq.Task, opts ...asynq.Option) error {
	if _, err := e.client.Enqueue(task, append([]asynq.Option{asynq.Queue(e.queue)}, opts...)...); err != nil {
		return fmt.Errorf("enqueue %s task: %w", task.Type(), err)
	}
	return nil
}

// EnqueueLongTask is EnqueueTask on this deployment's long-task queue, for a
// task that may hold a worker for hours.
func (e *Enqueuer) EnqueueLongTask(task *asynq.Task, opts ...asynq.Option) error {
	if _, err := e.client.Enqueue(task, append([]asynq.Option{asynq.Queue(LongQueue(e.queue))}, opts...)...); err != nil {
		return fmt.Errorf("enqueue %s task: %w", task.Type(), err)
	}
	return nil
}

// EnqueueAnalysis dispatches a TaskAnalysis to the asynq queue. The
// analyses row must already exist with status='pending' (created by the
// POST handler); the worker loads it tenant-scoped via the payload.
//
// Same MaxRetry as pipeline tasks. Timeout is shorter — analyses read
// already-stored data, no engine calls, so they finish in seconds even
// for big projects.
//
// The task ID is derived from the analysis, so while one task for an analysis
// is queued or running, a second enqueue is dropped as a duplicate (the auto
// analysis is promoted from several places at once). The worker's claim in
// analysis.Service.Run is the real guard; this only saves the extra task.
func (e *Enqueuer) EnqueueAnalysis(analysisID, tenantID int64) error {
	task, err := NewAnalysisTask(AnalysisPayload{AnalysisID: analysisID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("create analysis task: %w", err)
	}
	if _, err := e.client.Enqueue(task,
		asynq.Queue(e.queue),
		asynq.TaskID(AnalysisTaskID(analysisID)),
		asynq.MaxRetry(3),
		asynq.Timeout(10*time.Minute),
	); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return fmt.Errorf("enqueue analysis task: %w", err)
	}
	return nil
}

// AnalysisTaskID is the asynq task ID for an analysis run.
func AnalysisTaskID(analysisID int64) string {
	return "analysis:" + strconv.FormatInt(analysisID, 10)
}
