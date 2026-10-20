package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/httputil"
	"openloc/internal/pipeline"
)

// Between asynq attempts a failed run reads as running (queued + a retry
// label), not failed: otherwise Re-run or Complete on the "errored" task races
// asynq's own retry. Only the last attempt marks the job failed.
func TestFailRun_OnlyTheLastAttemptMarksTheJobFailed(t *testing.T) {
	orig := isLastAttempt
	defer func() { isLastAttempt = orig }()
	p := PipelinePayload{JobID: 42, TenantID: 7}

	isLastAttempt = func(context.Context) bool { return false }
	mq := &workerMockQuerier{}
	NewHandler(&stubPipeline{}, mq).failRun(context.Background(), p, 0, pipeline.PhaseQEAPE, fmt.Errorf("provider 503"))
	if len(mq.errorCalls) != 0 {
		t.Fatalf("a non-final attempt must not mark the job failed, got %d UpdateJobError calls", len(mq.errorCalls))
	}
	if len(mq.retryingCalls) != 1 {
		t.Fatalf("a non-final attempt must mark the job retrying, got %d calls", len(mq.retryingCalls))
	}
	got := mq.retryingCalls[0]
	if got.ID != 42 || !pipeline.IsAIStepLabel(got.CurrentStep) || !pipeline.IsPipelineRunning("queued", got.CurrentStep) {
		t.Errorf("retry state = %+v; want job 42 with a running AI-step label", got)
	}
	if got.ErrorMessage == nil || !strings.Contains(*got.ErrorMessage, "provider 503") {
		t.Errorf("retry state keeps the attempt's error, got %v", got.ErrorMessage)
	}

	isLastAttempt = func(context.Context) bool { return true }
	mq = &workerMockQuerier{}
	NewHandler(&stubPipeline{}, mq).failRun(context.Background(), p, 0, pipeline.PhaseQEAPE, fmt.Errorf("provider 503"))
	if len(mq.errorCalls) != 1 || len(mq.retryingCalls) != 0 {
		t.Fatalf("the last attempt marks the job failed: UpdateJobError=%d MarkJobRetrying=%d", len(mq.errorCalls), len(mq.retryingCalls))
	}
}

// A failure every retry would hit again (pipeline.Permanent) fails the job on
// the first attempt and tells asynq not to retry. Retried, it would read
// "Retrying…" through the whole 1m/2m/4m backoff and only then fail.
func TestHandlePipeline_PermanentErrorFailsOnTheFirstAttempt(t *testing.T) {
	orig := isLastAttempt
	defer func() { isLastAttempt = orig }()
	isLastAttempt = func(context.Context) bool { return false }

	refused := httputil.NewClientError("the engine does not translate en → de")
	mq := &workerMockQuerier{}
	h := NewHandler(&stubPipeline{translateErr: pipeline.Permanent(refused)}, mq)
	task := makeTask(TaskPipeline, PipelinePayload{
		FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
		JobID: 42, TenantID: 7, Phases: pipeline.TranslatePhases{UseMT: true},
	})

	err := h.HandlePipeline(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("a permanent failure must tell asynq not to retry, got %v", err)
	}
	if len(mq.retryingCalls) != 0 || len(mq.errorCalls) != 1 {
		t.Fatalf("a permanent failure marks the job failed at once: MarkJobRetrying=%d UpdateJobError=%d",
			len(mq.retryingCalls), len(mq.errorCalls))
	}
	if got := mq.errorCalls[0].ErrorMessage; got == nil || *got != refused.Error() {
		t.Errorf("error_message = %v, want the refusal %q alone", got, refused.Error())
	}
}

// The error tracker reports a task once asynq is done with it. A SkipRetry
// ends it on the first attempt, so it is terminal there too, or its notice
// never fires.
func TestTerminalFailure(t *testing.T) {
	cases := []struct {
		name            string
		retried, maxRtr int
		err             error
		want            bool
	}{
		{"first attempt of three", 0, 3, errors.New("provider 503"), false},
		{"last attempt", 3, 3, errors.New("provider 503"), true},
		{"skip retry on the first attempt", 0, 3, fmt.Errorf("refused: %w", asynq.SkipRetry), true},
	}
	for _, c := range cases {
		if got := terminalFailure(c.retried, c.maxRtr, c.err); got != c.want {
			t.Errorf("%s: terminalFailure = %v, want %v", c.name, got, c.want)
		}
	}
}
