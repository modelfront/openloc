package workflow

// events.go — project_events emission for workflow-step actions

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
)

// emitStepEvent records a project_events row for a workflow-step action,
// attributed to the request's tenant.
func (h *Handler) emitStepEvent(ctx context.Context, eventType string, jobID int64, details map[string]any) {
	h.svc.emitStepEvent(ctx, auth.TenantIDFromContext(ctx), eventType, jobID, details)
}

// tsOrNil renders a pgtype timestamp as RFC 3339 or nil for event details.
func tsOrNil(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format(time.RFC3339)
}
