package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/projectevents"
)

// MetadataPatch is a partial update of a project's metadata, shared by
// PATCH /api/projects/{id} and the MCP update_project tool. A nil field keeps
// its current value.
type MetadataPatch struct {
	Name        *string
	Description *string
	// Status is one of projectStatuses except draft; a draft leaves its
	// status only through Publish.
	Status *string
	// CustomerID: 0 clears, a positive id sets (verified in the tenant).
	CustomerID *int64
	// Deadline: "" clears, otherwise an RFC 3339 datetime.
	Deadline *string
	Client   *string
	Domain   *string
}

// UpdateMetadata applies the patch and records a project.metadata_updated
// event when anything changed. Caller mistakes come back as
// httputil.ClientError; an unknown or foreign project as pgx.ErrNoRows.
func UpdateMetadata(ctx context.Context, q db.Querier, tenantID, id int64, p MetadataPatch) (db.Project, error) {
	params := db.UpdateProjectMetadataParams{ID: id, TenantID: tenantID}

	if p.Name != nil {
		trimmed := strings.TrimSpace(*p.Name)
		if trimmed == "" {
			return db.Project{}, httputil.NewClientError("name must not be empty")
		}
		params.Name = &trimmed
	}
	params.Description = p.Description
	// Client / domain: "" is the documented clear; the predict and feedback
	// metadata builders treat it like NULL.
	if p.Client != nil {
		trimmed := strings.TrimSpace(*p.Client)
		params.Client = &trimmed
	}
	if p.Domain != nil {
		trimmed := strings.TrimSpace(*p.Domain)
		params.Domain = &trimmed
	}

	if p.Status != nil {
		s := strings.TrimSpace(*p.Status)
		if !validProjectStatuses[s] {
			return db.Project{}, httputil.NewClientError(invalidStatusMessage)
		}
		if s == string(db.ProjectStatusDraft) {
			return db.Project{}, httputil.NewClientError("a project cannot be moved back to draft")
		}
		params.Status = db.NullProjectStatus{ProjectStatus: db.ProjectStatus(s), Valid: true}
	}

	if p.CustomerID != nil {
		switch n := *p.CustomerID; {
		case n == 0:
			params.ClearCustomer = true
		case n < 0:
			return db.Project{}, httputil.NewClientError("invalid customer_id (must be null, 0, or positive integer)")
		default:
			if _, err := q.GetCustomer(ctx, db.GetCustomerParams{ID: n, TenantID: tenantID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return db.Project{}, httputil.NewClientError("customer not found in this tenant")
				}
				return db.Project{}, fmt.Errorf("verify customer: %w", err)
			}
			params.CustomerID = &n
		}
	}

	if p.Deadline != nil {
		ts, err := parseDeadline(*p.Deadline)
		if err != nil {
			return db.Project{}, httputil.NewClientError("invalid deadline (must be ISO 8601, e.g. 2026-05-08T17:00:00Z)")
		}
		if ts.Valid {
			params.Deadline = ts
		} else {
			params.ClearDeadline = true
		}
	}

	// The old row is the "from" side of the audit diff and the tenant gate.
	oldProject, err := q.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Project{}, err
	}
	// Publish is what validates the required fields and turns a draft's files
	// into jobs, so a status change on a draft would skip both.
	if oldProject.Status == db.ProjectStatusDraft && params.Status.Valid {
		return db.Project{}, httputil.NewClientError("publish the draft instead of changing its status")
	}

	project, err := q.UpdateProjectMetadata(ctx, params)
	if err != nil {
		return db.Project{}, err
	}

	if changes := projectevents.Diff(projectMetaMap(oldProject), projectMetaMap(project)); changes != nil {
		if ch, ok := changes["customer_id"]; ok {
			changes["customer_name"] = projectevents.Change{
				From: customerName(ctx, q, tenantID, idFromChange(ch.From)),
				To:   customerName(ctx, q, tenantID, idFromChange(ch.To)),
			}
		}
		actorID, actorName := projectevents.ActorFromContext(ctx)
		projectevents.Emit(ctx, q, projectevents.Event{
			ProjectID: project.ID,
			TenantID:  tenantID,
			Type:      projectevents.TypeMetadataUpdated,
			ActorID:   actorID,
			ActorName: actorName,
			Details:   map[string]any{"changes": changes},
		})
	}
	return project, nil
}
