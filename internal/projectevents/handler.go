package projectevents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/ptr"
)

// Handler serves the project history read API.
type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

// EventActor is the joined users row; null when the event was a system action
// (actor_id NULL from the start) or the user row was since deleted — in the
// deleted case details.actor_name still carries the emit-time snapshot.
type EventActor struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type EventView struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Actor     *EventActor     `json:"actor"`
	FileID    *int64          `json:"file_id"`
	JobID     *int64          `json:"job_id"`
	Details   json.RawMessage `json:"details"`
	CreatedAt string          `json:"created_at"`
}

// ListParams narrows and pages the history feed. Cursor is the id of the
// last event of the previous page (0 for the first); ActorID 0 means anyone;
// TypePrefix is an event_type prefix ("file." is the category,
// "file.uploaded" the exact type).
type ListParams struct {
	Cursor     int64
	Limit      int32
	ActorID    int64
	TypePrefix string
}

// Page is one page of the history feed, newest first.
type Page struct {
	Events     []EventView
	NextCursor int64
	HasMore    bool
}

// List returns a project's history, shared by GET /events and the MCP
// list_project_events tool. An unknown or foreign project is pgx.ErrNoRows,
// not an empty page: an empty page must mean "no events yet".
func List(ctx context.Context, q db.Querier, tenantID, projectID int64, p ListParams) (Page, error) {
	if _, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID}); err != nil {
		return Page{}, err
	}
	// The prefix is a literal, never a LIKE pattern.
	prefix := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(p.TypePrefix)
	rows, err := q.ListProjectEvents(ctx, db.ListProjectEventsParams{
		ProjectID:   projectID,
		TenantID:    tenantID,
		Cursor:      p.Cursor,
		Limit:       p.Limit + 1, // one extra row = has_more probe
		ActorID:     p.ActorID,
		EventPrefix: prefix,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list project events: %w", err)
	}

	page := Page{HasMore: len(rows) > int(p.Limit)}
	if page.HasMore {
		rows = rows[:p.Limit]
	}
	page.Events = make([]EventView, 0, len(rows))
	for _, row := range rows {
		item := EventView{
			ID:        row.ID,
			EventType: row.EventType,
			FileID:    row.FileID,
			JobID:     row.JobID,
			Details:   json.RawMessage(row.Details),
			CreatedAt: row.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if row.ActorID != nil && row.ActorName != nil {
			item.Actor = &EventActor{
				ID:    *row.ActorID,
				Name:  *row.ActorName,
				Email: ptr.Deref(row.ActorEmail),
			}
		}
		page.Events = append(page.Events, item)
		page.NextCursor = row.ID
	}
	return page, nil
}

// HandleList serves GET /api/projects/{projectID}/events — the project
// history feed, newest first, cursor-paginated on id. Malformed filters fall
// back to "no filter" rather than 400: they only narrow an authorized read.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	cursor := httputil.ParseCursor(r, 200)
	params := ListParams{Cursor: cursor.Cursor, Limit: cursor.Limit, TypePrefix: r.URL.Query().Get("type")}
	if raw := r.URL.Query().Get("actor_id"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			params.ActorID = n
		}
	}
	page, err := List(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), projectID, params)
	if err != nil {
		httputil.ServiceError(w, err, "list", "project events")
		return
	}
	httputil.Paginated(w, page.Events, page.NextCursor, page.HasMore, -1)
}
