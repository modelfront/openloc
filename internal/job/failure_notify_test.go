package job_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"openloc/internal/notification"
	"openloc/internal/pipeline"
)

type spyFailureNotifier struct {
	mu  sync.Mutex
	got []notification.JobFailure
}

func (s *spyFailureNotifier) NotifyJobFailed(_ context.Context, f notification.JobFailure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, f)
	return nil
}

// A run the user waits on (Apply MT, Re-run AI step) that fails sends one bell
// notification naming the user, with MT set only when the MT step failed.
func TestSyncRunFailureNotifies(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		err    error
		wantMT bool
	}{
		{"apply mt, engine fails", "mt-apply", &pipeline.MTFailureError{Err: errors.New("engine overloaded")}, true},
		{"apply mt, other failure", "mt-apply", errors.New("list untranslated: boom"), false},
		{"ai step fails", "ai-step", errors.New("predict: boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newDetachFixture(t, &fakePipeline{failWith: tc.err})
			spy := &spyFailureNotifier{}
			fx.h.SetFailureNotifier(spy)

			rec := httptest.NewRecorder()
			fx.r.ServeHTTP(rec, httptest.NewRequest("POST", fmt.Sprintf("/api/jobs/%d/%s", fx.jobID, tc.path), nil))
			if rec.Code < 400 {
				t.Fatalf("status %d, want a failure", rec.Code)
			}
			if len(spy.got) != 1 {
				t.Fatalf("want 1 notification, got %d", len(spy.got))
			}
			f := spy.got[0]
			if f.TenantID != fx.tenant || f.JobID != fx.jobID || f.StartedBy == nil || *f.StartedBy != fx.userID {
				t.Errorf("got %+v, want tenant %d job %d started by %d", f, fx.tenant, fx.jobID, fx.userID)
			}
			if f.MT != tc.wantMT {
				t.Errorf("MT = %v, want %v", f.MT, tc.wantMT)
			}
			if f.Reason != tc.err.Error() {
				t.Errorf("Reason = %q, want %q", f.Reason, tc.err.Error())
			}
		})
	}
}
