package testutil

import (
	"context"
	"encoding/json"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// ProjectEvent is one project History row with its details decoded. JSON
// numbers decode as float64.
type ProjectEvent struct {
	db.ListProjectEventsRow
	Data map[string]any
}

// projectEventsLimit is far above what one test writes. Reaching it fails the
// test rather than dropping the oldest rows.
const projectEventsLimit = 500

// ProjectEvents lists a project's History, newest first, keeping the event
// types that start with prefix: "" keeps all, a full type keeps that type.
func ProjectEvents(t testing.TB, q db.Querier, projectID, tenantID int64, prefix string) []ProjectEvent {
	t.Helper()
	rows, err := q.ListProjectEvents(context.Background(), db.ListProjectEventsParams{
		ProjectID: projectID, TenantID: tenantID, Limit: projectEventsLimit, EventPrefix: prefix,
	})
	if err != nil {
		t.Fatalf("ProjectEvents project %d: %v", projectID, err)
	}
	if len(rows) == projectEventsLimit {
		t.Fatalf("ProjectEvents project %d: %d rows, raise projectEventsLimit", projectID, len(rows))
	}
	out := make([]ProjectEvent, len(rows))
	for i, r := range rows {
		out[i].ListProjectEventsRow = r
		if err := json.Unmarshal(r.Details, &out[i].Data); err != nil {
			t.Fatalf("ProjectEvents %s details %s: %v", r.EventType, r.Details, err)
		}
	}
	return out
}
