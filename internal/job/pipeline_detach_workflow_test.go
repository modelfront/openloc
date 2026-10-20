package job_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/notification"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
	"openloc/internal/workflow"
)

// runThenLeave posts to the job's run route, and once the run is under way
// cancels the request (the user closes the tab) and lets the run finish.
func runThenLeave(t *testing.T, fx detachFixture, fake *fakePipeline, route string) {
	t.Helper()
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/%s", fx.jobID, route), nil).WithContext(reqCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fx.r.ServeHTTP(httptest.NewRecorder(), req)
	}()
	waitForStart(t, func() bool { mt, ai := fake.counts(); return mt+ai > 0 })
	cancelReq()
	close(fake.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never finished")
	}
}

// A user who leaves during an Apply MT run still gets the work after it: the
// Pre-translation step completes and the next step starts, as when they stay.
func TestMTApplyCompletesThePretranslateStepAfterTheClientLeft(t *testing.T) {
	fake := &fakePipeline{release: make(chan struct{})}
	fx := newDetachFixture(t, fake)
	ctx := context.Background()
	project := detachProject(t, fx)
	steps := testutil.SeedWorkflow(t, fx.q, project.SettingsProfileID, fx.jobID,
		testutil.WorkflowStepSpec{Name: "Pre-translation", StepType: "pretranslate", Status: "in_progress"},
		testutil.WorkflowStepSpec{Name: "Review", StepType: "human"},
	)
	fx.h.SetWorkflow(workflow.NewService(fx.q, pipeline.NewPgxTxRunner(fx.pool), nil))

	runThenLeave(t, fx, fake, "mt-apply")

	active, err := fx.q.GetActiveStepForJob(ctx, fx.jobID)
	if err != nil {
		t.Fatalf("active step: %v", err)
	}
	if active.ID != steps[1] {
		t.Errorf("active step = %d, want the review step %d: Pre-translation was left open", active.ID, steps[1])
	}
}

// A Re-run AI step the user left still records its history event.
func TestAIStepRecordsItsEventAfterTheClientLeft(t *testing.T) {
	fake := &fakePipeline{release: make(chan struct{})}
	fx := newDetachFixture(t, fake)
	project := detachProject(t, fx)

	runThenLeave(t, fx, fake, "ai-step")

	events, err := fx.q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{
		ProjectID: project.ID, TenantID: fx.tenant, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.EventType == "job.ai_apply_started" {
			return
		}
	}
	t.Errorf("no job.ai_apply_started event among %d, though the run finished", len(events))
}

// ctxSpyNotifier records the failure notices and whether their context was
// already cancelled when they were sent.
type ctxSpyNotifier struct {
	mu        sync.Mutex
	sent      int
	cancelled bool
}

func (s *ctxSpyNotifier) NotifyJobFailed(ctx context.Context, _ notification.JobFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent++
	s.cancelled = s.cancelled || ctx.Err() != nil
	return nil
}

// A run that fails after the user left still sends its failure notice, on a
// context the departure did not cancel.
func TestMTApplyFailureNotifiesAfterTheClientLeft(t *testing.T) {
	fake := &fakePipeline{release: make(chan struct{}), failAfter: errors.New("list untranslated: boom")}
	fx := newDetachFixture(t, fake)
	spy := &ctxSpyNotifier{}
	fx.h.SetFailureNotifier(spy)

	runThenLeave(t, fx, fake, "mt-apply")

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.sent != 1 || spy.cancelled {
		t.Errorf("failure notices = %d, sent on a cancelled context = %v; want 1 on a live one", spy.sent, spy.cancelled)
	}
}

// detachProject is the project of the fixture's job.
func detachProject(t *testing.T, fx detachFixture) db.Project {
	t.Helper()
	ctx := context.Background()
	jobRow, err := fx.q.GetJob(ctx, fx.jobID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := fx.q.GetFile(ctx, jobRow.FileID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := fx.q.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return project
}
