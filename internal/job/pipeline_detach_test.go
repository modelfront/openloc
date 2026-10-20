package job_test

// Pins that synchronous pipeline runs are detached from the request context
// (a browser refresh must not cancel them; only server draining does) and that
// the atomic double-start claim turns a second start into a 409.
//
// These use a fake PipelineService rather than testutil.NewTestRouter because
// the unit under test is the HANDLER's context/lifecycle handling — the fake
// lets a test block the pipeline mid-run and observe exactly which context it
// was given.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/job"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// fakePipeline blocks TranslateFile/RunAIStep until released (or until its ctx
// dies when blockOnCtx is set) and records what the handler's context looked
// like at that moment.
type fakePipeline struct {
	mu             sync.Mutex
	translateCalls int
	aiCalls        int
	sawCtxErr      error

	release    chan struct{} // non-nil: block until closed
	blockOnCtx bool          // block until ctx.Done() instead
	// progressLoop mirrors a long run: report progress every few ms until the
	// ctx dies, so a stop requested mid-run reaches the progress writer.
	progressLoop bool
	outcome      pipeline.TranslateOutcome // returned by TranslateFile on success
	aiResult     pipeline.AIStepResult     // returned by RunAIStep on success
	failWith     error                     // non-nil: TranslateFile and RunAIStep return it at once
	failAfter    error                     // non-nil: returned once released, in place of success
}

func (f *fakePipeline) wait(ctx context.Context, label string) error {
	if f.blockOnCtx {
		<-ctx.Done()
		f.mu.Lock()
		f.sawCtxErr = ctx.Err()
		f.mu.Unlock()
		return fmt.Errorf("%s aborted: %w", label, ctx.Err())
	}
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	f.sawCtxErr = ctx.Err()
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s aborted: %w", label, err)
	}
	return f.failAfter
}

// loopProgress reports progress until ctx dies, then returns the wrapped
// ctx error the real pipeline returns from a chunk-boundary abort.
func (f *fakePipeline) loopProgress(ctx context.Context, onProgress pipeline.ProgressFunc, phase, label string) error {
	done := 0
	for ctx.Err() == nil {
		done++
		if onProgress != nil {
			onProgress(phase, done, done+1)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
	}
	f.mu.Lock()
	f.sawCtxErr = ctx.Err()
	f.mu.Unlock()
	return fmt.Errorf("%s aborted: %w", label, ctx.Err())
}

func (f *fakePipeline) TranslateFile(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string, onProgress pipeline.ProgressFunc, phases pipeline.TranslatePhases) (pipeline.TranslateOutcome, error) {
	f.mu.Lock()
	f.translateCalls++
	f.mu.Unlock()
	if f.failWith != nil {
		return f.outcome, f.failWith
	}
	if f.progressLoop {
		return f.outcome, f.loopProgress(ctx, onProgress, "mt", "mt translate")
	}
	// Mirror the real pipeline's per-chunk progress callback (nil-guarded
	// there too) so tests can pin what the handler's progress writer
	// persists. Like the real pipeline, progress fires only when the phase
	// actually runs — a config-skipped phase never reaches the callback.
	if onProgress != nil && f.outcome.MTSkipReason == "" {
		onProgress("mt", 1, 2)
	}
	return f.outcome, f.wait(ctx, "mt translate")
}

func (f *fakePipeline) RunAIStep(ctx context.Context, tenantID, jobID int64, onProgress pipeline.ProgressFunc) (pipeline.AIStepResult, error) {
	f.mu.Lock()
	f.aiCalls++
	f.mu.Unlock()
	if f.failWith != nil {
		return pipeline.AIStepResult{}, f.failWith
	}
	if f.progressLoop {
		return pipeline.AIStepResult{}, f.loopProgress(ctx, onProgress, pipeline.PhaseQEAPE, "ai step")
	}
	return f.aiResult, f.wait(ctx, "ai step")
}

func (f *fakePipeline) ExportFile(context.Context, int64, int64, string) ([]byte, string, error) {
	return nil, "", nil
}

func (f *fakePipeline) ExportBilingualXLIFF(context.Context, int64, int64, string, string) ([]byte, string, error) {
	return nil, "", nil
}

func (f *fakePipeline) SaveApprovedToTM(context.Context, int64, int64, string, string) (pipeline.TMWriteResult, error) {
	return pipeline.TMWriteResult{}, nil
}

func (f *fakePipeline) MTPreview(context.Context, int64, int64, string, string, string, string, int64) (pipeline.MTPreviewOutput, error) {
	return pipeline.MTPreviewOutput{}, nil
}

func (f *fakePipeline) counts() (translate, ai int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.translateCalls, f.aiCalls
}

func (f *fakePipeline) ctxErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sawCtxErr
}

// waitForStart polls until the fake has been entered, so a test can cancel the
// request only after the pipeline is genuinely mid-run.
func waitForStart(t *testing.T, started func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !started() {
		if time.Now().After(deadline) {
			t.Fatal("pipeline never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// adminClaims are an org admin's session claims.
func adminClaims(tenantID, userID int64) *auth.Claims {
	return &auth.Claims{TenantID: tenantID, UserID: userID, Role: "admin", Scope: auth.ScopeTenant}
}

// detachRouter is the minimal stand-in for the real stack: claims middleware +
// the two synchronous pipeline routes.
func detachRouter(h *job.Handler, userID, tenantID int64) chi.Router {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithClaims(req.Context(), adminClaims(tenantID, userID))))
		})
	})
	r.Post("/api/jobs/{jobID}/mt-apply", h.HandleMTApply)
	r.Post("/api/jobs/{jobID}/ai-step", h.HandleAIStep)
	r.Post("/api/jobs/{jobID}/stop", h.HandleStopJob)
	return r
}

type detachFixture struct {
	q      db.Querier
	pool   *pgxpool.Pool
	h      *job.Handler
	fake   *fakePipeline
	r      chi.Router
	jobID  int64
	userID int64
	tenant int64
}

func newDetachFixture(t *testing.T, fake *fakePipeline) detachFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)

	tenant := testutil.SeedTenant(t, q, "DetachCo-"+t.Name())
	user := testutil.SeedUser(t, q, tenant.ID, "u@detach.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "Detach Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "f.txt", db.FileFormatTxt)
	jobRow := testutil.SeedJob(t, q, file.ID, "de")

	h := job.NewHandler(q, pool, fake, nil)
	return detachFixture{q: q, pool: pool, h: h, fake: fake, r: detachRouter(h, user.ID, tenant.ID), jobID: jobRow.ID, userID: user.ID, tenant: tenant.ID}
}

// TestMTApplySurvivesRequestCancel — the reported bug. Canceling the request
// mid-run (the refresh) must not cancel the context the pipeline runs on, and
// the job must finish as if nothing happened.
func TestMTApplySurvivesRequestCancel(t *testing.T) {
	fake := &fakePipeline{release: make(chan struct{})}
	fx := newDetachFixture(t, fake)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fx.r.ServeHTTP(rec, req)
	}()

	waitForStart(t, func() bool { n, _ := fake.counts(); return n > 0 })
	cancelReq() // the browser refresh
	close(fake.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never finished")
	}

	if err := fake.ctxErr(); err != nil {
		t.Errorf("the pipeline's context died with the client: %v — the run must be detached", err)
	}
	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if string(post.Status) == "failed" {
		msg := ""
		if post.ErrorMessage != nil {
			msg = *post.ErrorMessage
		}
		t.Errorf("job marked failed by a client disconnect (error_message=%q)", msg)
	}
	if post.CurrentStep != "mt-apply complete" {
		t.Errorf("current_step = %q, want %q — the run should have completed", post.CurrentStep, "mt-apply complete")
	}
}

// TestAIStepSurvivesRequestCancel — the same detach guarantee for the
// ai-step endpoint.
func TestAIStepSurvivesRequestCancel(t *testing.T) {
	fake := &fakePipeline{release: make(chan struct{})}
	fx := newDetachFixture(t, fake)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/ai-step", fx.jobID), nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fx.r.ServeHTTP(rec, req)
	}()

	waitForStart(t, func() bool { _, n := fake.counts(); return n > 0 })
	cancelReq()
	close(fake.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never finished")
	}

	if err := fake.ctxErr(); err != nil {
		t.Errorf("the AI step's context died with the client: %v — the run must be detached", err)
	}
	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if string(post.Status) == "failed" {
		t.Error("job marked failed by a client disconnect")
	}
}

// TestMTApplyAbortsOnDraining — the one thing that must still cancel a
// detached run is server shutdown, and the resulting job error must be a
// sentence a user can act on, not a Go context error.
func TestMTApplyAbortsOnDraining(t *testing.T) {
	fake := &fakePipeline{blockOnCtx: true}
	fx := newDetachFixture(t, fake)
	drain := make(chan struct{})
	fx.h.SetDraining(drain)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq() // unblocks the fake if the fix isn't in place
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fx.r.ServeHTTP(rec, req)
	}()

	waitForStart(t, func() bool { n, _ := fake.counts(); return n > 0 })
	close(drain) // the deploy
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never finished after draining")
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 for a drain-aborted run", rec.Code)
	}
	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if string(post.Status) != "failed" {
		t.Errorf("status = %q, want failed — the run genuinely did not finish", post.Status)
	}
	if post.ErrorMessage == nil {
		t.Fatal("error_message not set")
	}
	if strings.Contains(*post.ErrorMessage, "context canceled") {
		t.Errorf("error_message = %q — raw Go error shown to the user", *post.ErrorMessage)
	}
	if !strings.Contains(strings.ToLower(*post.ErrorMessage), "run") {
		t.Errorf("error_message = %q, want an actionable 'run it again' hint", *post.ErrorMessage)
	}
}

// TestMTApplyDoubleStartReturns409 — with runs surviving disconnects, the user
// can reload and click Apply MT again while the first run is still going. The
// claim must reject the second run atomically, before any pipeline work.
func TestMTApplyDoubleStartReturns409(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()

	if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: fx.jobID, Status: "in_progress", CurrentStep: "Running MT"}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while a run is in progress (body: %s)", rec.Code, rec.Body.String())
	}
	if n, _ := fake.counts(); n != 0 {
		t.Errorf("pipeline was called %d times behind an active run, want 0", n)
	}
}

// TestMTApplyAfterCompletedStepIsAllowed — the claim's "%complete" escape:
// a job whose previous phase FINISHED (status in_progress, step "… complete")
// is not running, and a re-run must go through.
func TestMTApplyAfterCompletedStepIsAllowed(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()

	if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: fx.jobID, Status: "in_progress", CurrentStep: "tm-apply complete"}); err != nil {
		t.Fatalf("seed completed-step job: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a finished phase must not block a re-run (body: %s)", rec.Code, rec.Body.String())
	}
	if n, _ := fake.counts(); n != 1 {
		t.Errorf("pipeline called %d times, want 1", n)
	}
}

// TestMTApplyPreflight400PreservesFailedState — the eager claim clears
// error_message; a pre-flight 400 (nothing ran) must put the job back EXACTLY
// as it was, message included. Without this, retrying a failed job against a
// now-unconfigured engine leaves an unexplained red "failed" badge.
func TestMTApplyPreflight400PreservesFailedState(t *testing.T) {
	fake := &fakePipeline{outcome: pipeline.TranslateOutcome{MTSkipReason: pipeline.PhaseSkipNoAssignment}}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()

	prior := "Machine translation failed: the provider is out of quota."
	if err := fx.q.UpdateJobError(ctx, db.UpdateJobErrorParams{ID: fx.jobID, ErrorMessage: &prior}); err != nil {
		t.Fatalf("seed failed job: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a config-missing phase (body: %s)", rec.Code, rec.Body.String())
	}

	post, err := fx.q.GetJob(ctx, fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if string(post.Status) != "failed" {
		t.Errorf("status = %q, want the prior 'failed' restored", post.Status)
	}
	if post.ErrorMessage == nil || *post.ErrorMessage != prior {
		got := "<nil>"
		if post.ErrorMessage != nil {
			got = *post.ErrorMessage
		}
		t.Errorf("error_message = %q, want the prior message preserved: %q", got, prior)
	}
}

// TestMTApplyStaleClaimIsReclaimable — a pod hard-killed mid-run leaves the
// job in_progress with a non-complete step, and re-clicking Apply MT must
// still recover it: the claim's staleness escape makes a row untouched for
// >30 minutes reclaimable. (Live runs
// bump updated_at at least once per 30-segment chunk via the progress writer,
// so a healthy run can never be stolen.)
func TestMTApplyStaleClaimIsReclaimable(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()

	if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: fx.jobID, Status: "in_progress", CurrentStep: "Running MT"}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}
	// The crashed pod: nothing has touched the row for 31 minutes.
	if _, err := fx.pool.Exec(ctx, "UPDATE jobs SET updated_at = now() - interval '31 minutes' WHERE id = $1", fx.jobID); err != nil {
		t.Fatalf("age the claim: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a stale (crashed) claim must be reclaimable (body: %s)", rec.Code, rec.Body.String())
	}
	if n, _ := fake.counts(); n != 1 {
		t.Errorf("pipeline called %d times, want 1", n)
	}
}

// TestMTApplySyncRunWritesProgress — the sync path's progress writer persists
// per-chunk counts (FE poll shows live progress, and each write bumps
// updated_at so the staleness escape can't steal a healthy long run).
func TestMTApplySyncRunWritesProgress(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.TranslatedCount == nil || *post.TranslatedCount != 1 || post.TotalCount == nil || *post.TotalCount != 2 {
		t.Errorf("progress = (%v, %v), want (1, 2) persisted from the pipeline's callback", post.TranslatedCount, post.TotalCount)
	}
}

// TestAIStepDoubleStartReturns409 — HandleAIStep's claim is a real
// double-start guard, not just a status write.
func TestAIStepDoubleStartReturns409(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()

	if err := fx.q.UpdateJobStatus(ctx, db.UpdateJobStatusParams{ID: fx.jobID, Status: "in_progress", CurrentStep: "Running QE+APE"}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/ai-step", fx.jobID), nil))

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while a run is in progress (body: %s)", rec.Code, rec.Body.String())
	}
	if _, n := fake.counts(); n != 0 {
		t.Errorf("RunAIStep was called %d times behind an active run, want 0", n)
	}
}

// --- Cooperative stop of a synchronous run (POST /jobs/{id}/stop) ---

// stopMidRun starts the given phase endpoint, waits until the fake is inside
// the run, requests a stop, and returns the phase response once the run ends.
func stopMidRun(t *testing.T, fx detachFixture, endpoint string, started func() bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/%s", fx.jobID, endpoint), nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fx.r.ServeHTTP(rec, req)
	}()
	waitForStart(t, started)

	stopRec := httptest.NewRecorder()
	fx.r.ServeHTTP(stopRec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/stop", fx.jobID), nil))
	if stopRec.Code != http.StatusAccepted {
		t.Fatalf("stop: expected 202, got %d (%s)", stopRec.Code, stopRec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never ended after the stop request")
	}
	return rec
}

// A stop mid-run ends the job as 'stopped' — not 'failed', not "interrupted by
// a server restart" — with a resumable label and no pending flag.
func TestMTApplyStopMidRun_EndsStoppedNotFailed(t *testing.T) {
	fake := &fakePipeline{progressLoop: true}
	fx := newDetachFixture(t, fake)

	rec := stopMidRun(t, fx, "mt-apply", func() bool { n, _ := fake.counts(); return n > 0 })
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stopped":true`) {
		t.Fatalf("expected 200 with stopped:true, got %d %s", rec.Code, rec.Body.String())
	}
	if fake.ctxErr() == nil {
		t.Error("the run's context must be cancelled by the stop")
	}
	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.Status != "stopped" {
		t.Errorf("status = %q, want stopped", post.Status)
	}
	if post.ErrorMessage != nil {
		t.Errorf("a user stop is not an error, got error_message %q", *post.ErrorMessage)
	}
	if !strings.HasPrefix(post.CurrentStep, "Stopped") || strings.HasSuffix(strings.ToLower(post.CurrentStep), "complete") {
		t.Errorf("current_step = %q, want a Stopped label that does not read as a finished phase", post.CurrentStep)
	}
	if post.StopRequestedAt.Valid {
		t.Error("stop_requested_at must be cleared on the terminal write")
	}
	if post.TranslatedCount == nil || *post.TranslatedCount < 1 {
		t.Errorf("progress written before the stop must survive, got %v", post.TranslatedCount)
	}
}

// Stopping a QE+APE run releases the editor lock (status leaves the running set).
func TestAIStepStopMidRun_ReleasesEditorLock(t *testing.T) {
	fake := &fakePipeline{progressLoop: true}
	fx := newDetachFixture(t, fake)

	rec := stopMidRun(t, fx, "ai-step", func() bool { _, n := fake.counts(); return n > 0 })
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stopped":true`) {
		t.Fatalf("expected 200 with stopped:true, got %d %s", rec.Code, rec.Body.String())
	}
	post, err := fx.q.GetJob(context.Background(), fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if pipeline.IsPipelineRunning(post.Status, post.CurrentStep) {
		t.Errorf("job still reads as running after the stop (status=%q step=%q) — the editor lock would stay engaged", post.Status, post.CurrentStep)
	}
	if !strings.Contains(post.CurrentStep, "Re-run AI step") {
		t.Errorf("QE+APE stop label should point at Re-run AI step, got %q", post.CurrentStep)
	}
}

// 'stopped' is outside the running predicate, so Apply MT re-claims the job and
// resumes; the claim clears any stale flag.
func TestMTApplyAfterStopIsAllowed(t *testing.T) {
	fake := &fakePipeline{}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()
	if err := fx.q.MarkJobStopped(ctx, db.MarkJobStoppedParams{ID: fx.jobID, CurrentStep: "Stopped — 1 / 2 segments translated; run Pre-translate again to resume"}); err != nil {
		t.Fatalf("seed stopped job: %v", err)
	}

	rec := httptest.NewRecorder()
	fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/mt-apply", fx.jobID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Apply MT after a stop must be allowed, got %d (%s)", rec.Code, rec.Body.String())
	}
	post, err := fx.q.GetJob(ctx, fx.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if post.Status != "in_progress" || post.CurrentStep != "mt-apply complete" {
		t.Errorf("after resume: status=%q step=%q, want in_progress / mt-apply complete", post.Status, post.CurrentStep)
	}
}
