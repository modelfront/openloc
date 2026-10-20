package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"
	"openloc/internal/enginehealth"

	"openloc/internal/notification"
	"openloc/internal/pipeline"
)

type spyFailureNotifier struct{ got []notification.JobFailure }

func (s *spyFailureNotifier) NotifyJobFailed(_ context.Context, f notification.JobFailure) error {
	s.got = append(s.got, f)
	return nil
}

// A job that fails for good tells the bell once, says whether MT was the cause,
// and names who started it. A failure asynq will retry stays quiet.
func TestFailRunNotifiesOnFinalFailure(t *testing.T) {
	orig := isLastAttempt
	defer func() { isLastAttempt = orig }()
	starter := int64(11)

	cases := []struct {
		name   string
		err    error
		last   bool
		want   int
		wantMT bool
	}{
		{"mt failure, final", &pipeline.MTFailureError{Err: errors.New("engine overloaded")}, true, 1, true},
		{"other failure, final", errors.New("list untranslated: boom"), true, 1, false},
		{"failure that will be retried", errors.New("boom"), false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isLastAttempt = func(context.Context) bool { return tc.last }
			spy := &spyFailureNotifier{}
			h := NewHandler(&stubPipeline{translateErr: tc.err}, &workerMockQuerier{}).WithFailureNotifier(spy)
			task := makeTask(TaskPipeline, PipelinePayload{
				FileID: 1, SourceLanguage: "en", TargetLanguage: "de",
				JobID: 42, TenantID: 7, TriggeredBy: &starter, Phases: pipeline.TranslatePhases{UseMT: true},
			})
			if err := h.HandlePipeline(context.Background(), task); err == nil {
				t.Fatal("expected the pipeline error to propagate")
			}
			if len(spy.got) != tc.want {
				t.Fatalf("want %d notifications, got %d", tc.want, len(spy.got))
			}
			if tc.want == 0 {
				return
			}
			f := spy.got[0]
			if f.TenantID != 7 || f.JobID != 42 || f.StartedBy == nil || *f.StartedBy != starter {
				t.Errorf("got %+v", f)
			}
			if f.MT != tc.wantMT {
				t.Errorf("MT = %v, want %v", f.MT, tc.wantMT)
			}
			if f.Reason != tc.err.Error() {
				t.Errorf("Reason = %q, want the job's error %q", f.Reason, tc.err.Error())
			}
		})
	}
}

// An engine that cannot run (no key, switched off) fails the same way on every
// try, so the task is not retried and the bell is sent on the first attempt.
func TestUnusableEngineFailsAtOnce(t *testing.T) {
	orig := isLastAttempt
	defer func() { isLastAttempt = orig }()
	isLastAttempt = func(context.Context) bool { return false } // asynq would retry

	starter := int64(11)
	unusable := &pipeline.MTFailureError{Err: pipeline.Permanent(fmt.Errorf("Machine translation failed: %w",
		&pipeline.EngineUnusableError{EngineName: "OpenAI translation", Reason: enginehealth.NoKey}))}
	cases := []struct {
		name string
		run  func(h *Handler) error
	}{
		{"pipeline", func(h *Handler) error {
			return h.HandlePipeline(context.Background(), makeTask(TaskPipeline, PipelinePayload{
				FileID: 1, SourceLanguage: "en", TargetLanguage: "de", JobID: 42, TenantID: 7,
				TriggeredBy: &starter, Phases: pipeline.TranslatePhases{UseMT: true},
			}))
		}},
		{"ai step", func(h *Handler) error {
			return h.HandleAIStep(context.Background(), makeTask(TaskAIStep, PipelinePayload{JobID: 42, TenantID: 7, TriggeredBy: &starter}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyFailureNotifier{}
			h := NewHandler(&stubPipeline{translateErr: unusable, aiStepErr: unusable}, &workerMockQuerier{}).WithFailureNotifier(spy)
			err := tc.run(h)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Errorf("want asynq.SkipRetry so the task is not retried, got %v", err)
			}
			if len(spy.got) != 1 {
				t.Fatalf("want the bell on the first attempt, got %d notifications", len(spy.got))
			}
		})
	}
}
