package worker_test

// The asynq ErrorHandler's Honeybadger notices must carry the triggering
// user's identity — user_id from the task payload's triggered_by, user_email
// resolved via the querier — matching what the HTTP middlewares put on
// session- and PAT-authenticated request notices.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/testutil"
	"openloc/internal/worker"
)

func TestIntegrationErrorHandler_NoticeCarriesUserIDAndEmail(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	rec := errtracktest.Install(t)

	tenant := testutil.SeedTenant(t, q, "WorkerNoticeCo")
	user := testutil.SeedUser(t, q, tenant.ID, "trigger@worker-notice.test", "pass")

	payload, err := json.Marshal(map[string]any{
		"tenant_id":    tenant.ID,
		"triggered_by": user.ID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task := asynq.NewTask("pipeline:translate", payload)

	// A bare context has no retry metadata → retried 0 of max 0 → terminal,
	// so the handler reports rather than logging a retry.
	worker.ErrorHandler(q).HandleError(context.Background(), task, errors.New("boom"))

	n := rec.One(t)
	// logging Attr helpers render IDs as strings (see logging/fields.go).
	if got := n.Context[errtrack.ContextUserID]; got != strconv.FormatInt(user.ID, 10) {
		t.Errorf("notice user_id = %v (%T), want %q", got, got, strconv.FormatInt(user.ID, 10))
	}
	if n.Context[errtrack.ContextUserEmail] != "trigger@worker-notice.test" {
		t.Errorf("notice user_email = %v, want the triggering user's email", n.Context[errtrack.ContextUserEmail])
	}
}

// System tasks (no triggered_by) must still report — with tenant context but
// no user identity, and crucially no failed-lookup noise.
func TestIntegrationErrorHandler_SystemTaskNoticeHasNoUser(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	rec := errtracktest.Install(t)
	tenant := testutil.SeedTenant(t, q, "WorkerSystemNoticeCo")

	payload, err := json.Marshal(map[string]any{"tenant_id": tenant.ID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	worker.ErrorHandler(q).HandleError(context.Background(), asynq.NewTask("pipeline:translate", payload), errors.New("boom"))

	n := rec.One(t)
	if _, ok := n.Context[errtrack.ContextUserEmail]; ok {
		t.Errorf("system task notice must not carry user_email, got %v", n.Context[errtrack.ContextUserEmail])
	}
}
