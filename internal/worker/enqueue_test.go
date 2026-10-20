package worker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"openloc/internal/pipeline"
)

// stubAsynqClient records enqueued tasks and satisfies asynqEnqueueClient so the
// Enqueuer's pre-enqueue job-status writes can be asserted without a live Redis.
type stubAsynqClient struct {
	tasks []*asynq.Task
	// opts per enqueue, so the queue namespacing can be asserted.
	opts [][]asynq.Option
	// err, when set, is returned by every Enqueue.
	err error
}

func (s *stubAsynqClient) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	s.tasks = append(s.tasks, task)
	s.opts = append(s.opts, opts)
	if s.err != nil {
		return nil, s.err
	}
	return &asynq.TaskInfo{ID: "test-task"}, nil
}

// queueOf extracts the queue an enqueue targeted. asynq.Option is opaque, but
// its String() renders as `Queue("name")`, which is enough to pin the contract.
func queueOf(t *testing.T, opts []asynq.Option) string {
	t.Helper()
	for _, o := range opts {
		s := fmt.Sprint(o)
		if strings.HasPrefix(s, "Queue(") {
			return strings.TrimSuffix(strings.TrimPrefix(s, `Queue("`), `")`)
		}
	}
	t.Fatalf("no Queue option among %v — the task would land on the default queue, which every branch deployment shares", opts)
	return ""
}

// EnqueueAIStep must flip the job to queued + "Queued for QE+APE" BEFORE the
// task lands, so the enqueue→pickup window is visible in the UI and
// trips the editor lock. The label is the contract the frontend + the
// translation-handler guard both key off, so we assert it verbatim and also
// prove it satisfies the "running" predicate (contains QE+APE, not a *complete
// label).
//
// Anti-tautology RED proof: delete the UpdateJobStatus block in EnqueueAIStep —
// statusCalls is empty and the len==1 assertion flips.
func TestEnqueueAIStep_MarksJobQueuedForQEAPE(t *testing.T) {
	mq := &workerMockQuerier{}
	client := &stubAsynqClient{}
	e := &Enqueuer{client: client, queries: mq}

	if err := e.EnqueueAIStep(42, 7, nil); err != nil {
		t.Fatalf("EnqueueAIStep: %v", err)
	}

	if len(mq.statusCalls) != 1 {
		t.Fatalf("expected exactly 1 UpdateJobStatus call, got %d", len(mq.statusCalls))
	}
	got := mq.statusCalls[0]
	if got.ID != 42 {
		t.Errorf("status flip targeted wrong job: got %d, want 42", got.ID)
	}
	if got.Status != "queued" {
		t.Errorf("status = %q, want %q", got.Status, "queued")
	}
	want := "Queued for " + pipeline.AIStepMarker
	if got.CurrentStep != want {
		t.Errorf("current_step = %q, want %q", got.CurrentStep, want)
	}

	// The label must read as "AI step running" to the shared predicate: it
	// carries the marker and does not end in "complete" (mirror of
	// isAIStepRunning in web/src/lib/project-logic.ts). The marker is
	// mode-independent on purpose — the mode this step runs is not known yet
	// when the job is queued, and a QE-only step must lock the editor too.
	if !strings.Contains(got.CurrentStep, pipeline.AIStepMarker) || strings.HasSuffix(strings.ToLower(got.CurrentStep), "complete") {
		t.Errorf("current_step %q must be a running AI-step label", got.CurrentStep)
	}

	if len(client.tasks) != 1 {
		t.Fatalf("expected exactly 1 enqueued task, got %d", len(client.tasks))
	}
	if client.tasks[0].Type() != TaskAIStep {
		t.Errorf("enqueued task type = %q, want %q", client.tasks[0].Type(), TaskAIStep)
	}
}

// The status flip must happen before the enqueue, so a jobID of 0 (defensive;
// real callers always pass a job) skips the write rather than corrupting job 0.
func TestEnqueueAIStep_ZeroJobSkipsStatusWrite(t *testing.T) {
	mq := &workerMockQuerier{}
	client := &stubAsynqClient{}
	e := &Enqueuer{client: client, queries: mq}

	if err := e.EnqueueAIStep(0, 7, nil); err != nil {
		t.Fatalf("EnqueueAIStep: %v", err)
	}
	if len(mq.statusCalls) != 0 {
		t.Fatalf("jobID=0 must not write job status, got %d calls", len(mq.statusCalls))
	}
}

// Pins that producer and consumer resolve the same configured queue via
// QueueOrDefault. Non-master branches share one
// redis-staging and asynq has no key prefix, so each deployment needs its own
// queue — and a producer publishing to a queue nobody consumes is a silent
// backlog. Hence one helper, asserted from both sides.
func TestEnqueuer_PublishesToTheConfiguredQueue(t *testing.T) {
	for _, tc := range []struct{ configured, want string }{
		{"llm-batch", "llm-batch"},
		{"", "default"},          // unset → asynq's default
		{"  padded  ", "padded"}, // whitespace from an env var
	} {
		t.Run("queue="+tc.configured, func(t *testing.T) {
			if got := QueueOrDefault(tc.configured); got != tc.want {
				t.Fatalf("QueueOrDefault(%q) = %q, want %q", tc.configured, got, tc.want)
			}

			stub := &stubAsynqClient{}
			e := &Enqueuer{client: stub, queries: &workerMockQuerier{}, queue: QueueOrDefault(tc.configured)}
			if err := e.EnqueueAnalysis(1, 1); err != nil {
				t.Fatalf("EnqueueAnalysis: %v", err)
			}
			if len(stub.opts) != 1 {
				t.Fatalf("expected 1 enqueue, got %d", len(stub.opts))
			}
			if got := queueOf(t, stub.opts[0]); got != tc.want {
				t.Errorf("enqueued to %q, want %q", got, tc.want)
			}
		})
	}
}

// EnqueueAnalysis gives every task for one analysis the same asynq task ID, so
// asynq drops a second enqueue while the first is queued or running. The drop
// surfaces as ErrTaskIDConflict, which is the expected outcome, not a failure.
func TestEnqueueAnalysis_DedupesByAnalysisTaskID(t *testing.T) {
	client := &stubAsynqClient{}
	e := &Enqueuer{client: client, queue: "default"}

	if err := e.EnqueueAnalysis(42, 7); err != nil {
		t.Fatalf("EnqueueAnalysis: %v", err)
	}
	want := fmt.Sprint(asynq.TaskID(AnalysisTaskID(42)))
	found := false
	for _, o := range client.opts[0] {
		if fmt.Sprint(o) == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("want option %s among %v", want, client.opts[0])
	}
	if AnalysisTaskID(42) == AnalysisTaskID(43) {
		t.Fatal("task IDs of different analyses must differ")
	}

	client.err = asynq.ErrTaskIDConflict
	if err := e.EnqueueAnalysis(42, 7); err != nil {
		t.Errorf("duplicate enqueue: want nil (already queued), got %v", err)
	}

	client.err = fmt.Errorf("redis down")
	if err := e.EnqueueAnalysis(42, 7); err == nil {
		t.Error("a real enqueue failure must still be returned")
	}
}

// The QE+APE step runs at most 3 times in total: the first attempt and two
// retries. A vendor error that does not go away should fail the job within
// minutes, not keep it waiting.
func TestEnqueueAIStep_ThreeAttemptsInTotal(t *testing.T) {
	stub := &stubAsynqClient{}
	e := &Enqueuer{client: stub, queries: &workerMockQuerier{}, queue: "default"}
	if err := e.EnqueueAIStep(42, 7, nil); err != nil {
		t.Fatalf("EnqueueAIStep: %v", err)
	}
	if len(stub.opts) != 1 {
		t.Fatalf("expected 1 enqueue, got %d", len(stub.opts))
	}
	found := false
	for _, o := range stub.opts[0] {
		if o.Type() == asynq.MaxRetryOpt {
			found = true
			if got := o.Value(); got != 2 {
				t.Errorf("MaxRetry = %v, want 2 (3 attempts in total)", got)
			}
		}
	}
	if !found {
		t.Fatal("no MaxRetry option: asynq would retry 25 times")
	}
}
