//go:generate go run gen_mock_querier.go

package worker

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	db "openloc/internal/database/sqlcgen"
)

// workerMockQuerier captures calls to UpdateJobStatus, UpdateJobError, and
// UpdateJobProgress for assertion in tests. All other db.Querier methods
// are auto-generated panic stubs in mock_querier_stubs_test.go.
type workerMockQuerier struct {
	mu sync.Mutex

	// Captured context errors AT CALL TIME — the core assertion for the failJob context test.
	// We capture ctx.Err() during the call, not the context itself, because the caller's
	// defer cancel() runs after the call returns and would cancel the context before assertion.
	statusCtxErrs   []error
	errorCtxErrs    []error
	progressCtxErrs []error

	// Captured params
	statusCalls   []db.UpdateJobStatusParams
	errorCalls    []db.UpdateJobErrorParams
	progressCalls []db.UpdateJobProgressParams

	// Simulates deleted jobs for cooperative cancellation tests
	deletedJobIDs map[int64]struct{}
	// Simulates a pending user stop request (jobs.stop_requested_at set)
	stopRequestedJobIDs map[int64]struct{}
	// Captured MarkJobStopped params
	stoppedCalls []db.MarkJobStoppedParams
	// Captured StartJobRun params
	startRunCalls []db.StartJobRunParams
	// Captured MarkJobRetrying params
	retryingCalls []db.MarkJobRetryingParams
	// Saved outcomes of failed attempts, by job id
	runOutcomes map[int64][]byte

	// Predict-on-start chaining: control what the job's
	// active workflow step resolves to. activeStepWorkflowStepID is returned by
	// GetActiveStepForJob; activeStepAIRouterID is the AiRouterID on the
	// WorkflowStep that GetWorkflowStep returns (nil = plain human step).
	activeStepWorkflowStepID int64
	activeStepAIRouterID     *int64
	noActiveStep             bool // GetActiveStepForJob returns pgx.ErrNoRows

	// Auto-analysis promote-on-failure path. Configure these to make
	// PromoteProjectAutoAnalysisIfReady reach EnqueueAnalysis:
	//   GetFile → File{ProjectID: autoFileProjectID}
	//   GetJob  → Job{FileID: autoFileID}  (AI-step path)
	//   GetPendingAutoAnalysis → Analysis{ID: autoPendingAnalysisID} (0 → ErrNoRows)
	//   GetProject → Project{SettingsProfileID: autoSettingsProfileID}
	//   GetSettingsProfile → SettingsProfile{Tm/MtOnUpload} (both false → gate passes)
	//   ListProjectJobsForAutoAnalysis → autoProjectJobs
	autoFileID            int64
	autoFileProjectID     int64
	autoPendingAnalysisID int64
	autoSettingsProfileID int64
	autoTMOnUpload        bool
	autoMTOnUpload        bool
	autoProjectJobs       []db.ListProjectJobsForAutoAnalysisRow

	// Every task flushes its stage timings on the way out,
	// so this has to be implemented rather than a panic stub — and capturing
	// the params lets tests assert the worker actually recorded queue wait and
	// the whole-task span.
	timingCalls []db.UpsertJobTimingParams
}

func (m *workerMockQuerier) UpsertJobTiming(_ context.Context, arg db.UpsertJobTimingParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timingCalls = append(m.timingCalls, arg)
	return nil
}

// timings returns a copy of the captured flushes.
func (m *workerMockQuerier) timings() []db.UpsertJobTimingParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]db.UpsertJobTimingParams(nil), m.timingCalls...)
}

// mockJobFileID is the file every mock job belongs to; unit-test payloads that
// name a file name this one, so the handlers' payload check passes.
const mockJobFileID int64 = 1

// GetJobTenant serves the handlers' payload check: every job belongs to the
// payload's tenant. The cross-tenant rejection is pinned by
// payload_tenant_integration_test.go.
func (m *workerMockQuerier) GetJobTenant(_ context.Context, arg db.GetJobTenantParams) (db.Job, error) {
	return db.Job{ID: arg.ID, FileID: mockJobFileID}, nil
}

// Audit emissions resolve job→file tenant-scoped before inserting a
// project_events row. ErrNoRows makes the best-effort emission skip cleanly in
// these unit tests; the real emission is pinned by events_integration_test.go.
func (m *workerMockQuerier) GetFileTenant(_ context.Context, _ db.GetFileTenantParams) (db.File, error) {
	return db.File{}, pgx.ErrNoRows
}

func (m *workerMockQuerier) GetFile(_ context.Context, id int64) (db.File, error) {
	return db.File{ID: id, ProjectID: m.autoFileProjectID}, nil
}

func (m *workerMockQuerier) GetJob(_ context.Context, id int64) (db.Job, error) {
	return db.Job{ID: id, FileID: m.autoFileID}, nil
}

func (m *workerMockQuerier) GetPendingAutoAnalysis(_ context.Context, projectID int64) (db.Analysis, error) {
	if m.autoPendingAnalysisID == 0 {
		return db.Analysis{}, pgx.ErrNoRows
	}
	return db.Analysis{ID: m.autoPendingAnalysisID, ProjectID: projectID}, nil
}

func (m *workerMockQuerier) GetProject(_ context.Context, arg db.GetProjectParams) (db.Project, error) {
	return db.Project{ID: arg.ID, TenantID: arg.TenantID, SettingsProfileID: m.autoSettingsProfileID}, nil
}

func (m *workerMockQuerier) GetSettingsProfile(_ context.Context, _ int64) (db.SettingsProfile, error) {
	return db.SettingsProfile{TmOnUpload: m.autoTMOnUpload, MtOnUpload: m.autoMTOnUpload}, nil
}

func (m *workerMockQuerier) ListProjectJobsForAutoAnalysis(_ context.Context, _ int64) ([]db.ListProjectJobsForAutoAnalysisRow, error) {
	return m.autoProjectJobs, nil
}

func (m *workerMockQuerier) GetActiveStepForJob(ctx context.Context, jobID int64) (db.GetActiveStepForJobRow, error) {
	if m.noActiveStep {
		return db.GetActiveStepForJobRow{}, pgx.ErrNoRows
	}
	return db.GetActiveStepForJobRow{JobID: jobID, WorkflowStepID: m.activeStepWorkflowStepID}, nil
}

func (m *workerMockQuerier) GetWorkflowStep(ctx context.Context, id int64) (db.WorkflowStep, error) {
	return db.WorkflowStep{ID: id, AiRouterID: m.activeStepAIRouterID}, nil
}

func (m *workerMockQuerier) UpdateJobStatus(ctx context.Context, arg db.UpdateJobStatusParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusCtxErrs = append(m.statusCtxErrs, ctx.Err())
	m.statusCalls = append(m.statusCalls, arg)
	return nil
}

func (m *workerMockQuerier) UpdateJobError(ctx context.Context, arg db.UpdateJobErrorParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorCtxErrs = append(m.errorCtxErrs, ctx.Err())
	m.errorCalls = append(m.errorCalls, arg)
	return nil
}

func (m *workerMockQuerier) MarkJobRetrying(_ context.Context, arg db.MarkJobRetryingParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryingCalls = append(m.retryingCalls, arg)
	return nil
}

func (m *workerMockQuerier) SaveJobRunOutcome(_ context.Context, arg db.SaveJobRunOutcomeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runOutcomes == nil {
		m.runOutcomes = map[int64][]byte{}
	}
	m.runOutcomes[arg.JobID] = arg.Outcome
	return nil
}

func (m *workerMockQuerier) GetJobRunOutcome(_ context.Context, arg db.GetJobRunOutcomeParams) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.runOutcomes[arg.JobID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return raw, nil
}

func (m *workerMockQuerier) DeleteJobRunOutcome(_ context.Context, arg db.DeleteJobRunOutcomeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runOutcomes, arg.JobID)
	return nil
}

func (m *workerMockQuerier) UpdateJobProgress(ctx context.Context, arg db.UpdateJobProgressParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progressCtxErrs = append(m.progressCtxErrs, ctx.Err())
	m.progressCalls = append(m.progressCalls, arg)
	return nil
}

func (m *workerMockQuerier) GetJobRunState(_ context.Context, id int64) (db.GetJobRunStateRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, deleted := m.deletedJobIDs[id]; deleted {
		return db.GetJobRunStateRow{}, pgx.ErrNoRows
	}
	_, stop := m.stopRequestedJobIDs[id]
	return db.GetJobRunStateRow{StopRequested: stop}, nil
}

// StartJobRun refuses (0 rows) when a stop is pending or the job is deleted,
// like the real query.
func (m *workerMockQuerier) StartJobRun(_ context.Context, arg db.StartJobRunParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startRunCalls = append(m.startRunCalls, arg)
	if _, deleted := m.deletedJobIDs[arg.ID]; deleted {
		return 0, nil
	}
	if _, stop := m.stopRequestedJobIDs[arg.ID]; stop {
		return 0, nil
	}
	return 1, nil
}

func (m *workerMockQuerier) MarkJobStopped(_ context.Context, arg db.MarkJobStoppedParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stoppedCalls = append(m.stoppedCalls, arg)
	return nil
}

// SetJobMTSkipReason is a no-op: these unit tests don't read the reason back;
// the integration tests do.
func (m *workerMockQuerier) SetJobMTSkipReason(context.Context, db.SetJobMTSkipReasonParams) error {
	return nil
}
