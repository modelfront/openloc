package worker_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/ai"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/httputil"
	"openloc/internal/pipeline"
	"openloc/internal/worker"
)

// A task that failed for a reason the user fixes is already on the job (failed
// status, message, bell notification), so it sends no notice. Anything else is
// our fault and does. The errors are wrapped the way the worker's failRun hands
// them to asynq.
func TestErrorHandler_UserFacingFailuresSendNoNotice(t *testing.T) {
	final := func(err error) error { return fmt.Errorf("%w: %w", pipeline.Permanent(err), asynq.SkipRetry) }
	cases := []struct {
		name   string
		err    error
		notice bool
	}{
		{"missing configuration", final(httputil.NewClientError("No TM configured for en → es-mx — add one in project settings")), false},
		{"engine cannot run", final(fmt.Errorf("Machine translation failed: %w", &pipeline.EngineUnusableError{EngineName: "DeepL"})), false},
		{"vendor rejected the key", fmt.Errorf("translate: %w", &ai.ProviderError{Provider: ai.ProviderDeepL, Status: 403, Kind: ai.ProviderErrAuth}), false},
		{"vendor quota", fmt.Errorf("translate: %w", &ai.ProviderError{Provider: ai.ProviderDeepL, Status: 456, Kind: ai.ProviderErrQuota}), false},
		{"vendor down", fmt.Errorf("translate: %w", &pipeline.VendorOutageError{Message: "Machine translation failed: DeepL is having a problem right now.", Err: &ai.ProviderError{Provider: ai.ProviderDeepL, Status: 503, Kind: ai.ProviderErrProvider}}), true},
		{"vendor unreachable", fmt.Errorf("translate: %w", &ai.ProviderError{Provider: ai.ProviderDeepL, Kind: ai.ProviderErrUnreachable}), true},
		{"extension failed", fmt.Errorf("ai step: %w", &pipeline.ExtensionFailure{Message: "The AI step could not complete: QE did not respond. Try again.", Provider: "ext:qe", Err: errors.New("start extension: boom")}), true},
		{"our bug", errors.New("get project: connection reset"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := errtracktest.Install(t)
			// A bare context has no retry metadata, so the failure is terminal.
			worker.ErrorHandler(nil).HandleError(context.Background(), asynq.NewTask("pipeline:translate", []byte(`{"tenant_id":1}`)), c.err)
			if got := rec.Len() == 1; got != c.notice {
				t.Errorf("notice sent = %v, want %v (%d notices)", got, c.notice, rec.Len())
			}
		})
	}
}
