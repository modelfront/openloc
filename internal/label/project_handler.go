package label

import (
	"context"
	"fmt"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// picksRequest is the body of both PUT label endpoints: the whole set of
// picks. Replace rather than patch — the form holds every label row at once,
// so what the user sees is the request.
type picksRequest struct {
	Labels []struct {
		LabelDefinitionID int64 `json:"label_definition_id"`
		LabelValueID      int64 `json:"label_value_id"`
	} `json:"labels"`
}

// valueIDs drops the client's label_definition_id: the value decides the label.
func (req picksRequest) valueIDs() []int64 {
	ids := make([]int64, 0, len(req.Labels))
	for _, l := range req.Labels {
		ids = append(ids, l.LabelValueID)
	}
	return ids
}

// HandleListProjectLabels returns a project's picks, label name order.
func (h *Handler) HandleListProjectLabels(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	out, err := ProjectPicks(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), projectID)
	if err != nil {
		httputil.ServerError(w, "list", "project labels")
		return
	}
	httputil.Data(w, out)
}

// HandleSetProjectLabels replaces a project's whole set of picks.
func (h *Handler) HandleSetProjectLabels(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req picksRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	out, err := SetProjectPicks(r.Context(), h.pool, h.queries, auth.TenantIDFromContext(r.Context()), projectID, req.valueIDs())
	if err != nil {
		httputil.ServiceError(w, err, "set", "project")
		return
	}
	httputil.Data(w, out)
}

// HandleListTemplateLabels returns a template's presets — the labels a project
// started from it begins with.
func (h *Handler) HandleListTemplateLabels(w http.ResponseWriter, r *http.Request) {
	templateID, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	out, err := templatePicks(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), templateID)
	if err != nil {
		httputil.ServerError(w, "list", "template labels")
		return
	}
	httputil.Data(w, out)
}

// HandleSetTemplateLabels replaces a template's whole set of presets, in one
// transaction.
func (h *Handler) HandleSetTemplateLabels(w http.ResponseWriter, r *http.Request) {
	templateID, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	var req picksRequest
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	tenantID := auth.TenantIDFromContext(ctx)
	if _, err := h.queries.GetTemplate(ctx, db.GetTemplateParams{ID: templateID, TenantID: tenantID}); httputil.DBError(w, err, "template") {
		return
	}
	picks, err := resolvePicks(ctx, h.queries, tenantID, req.valueIDs())
	if err != nil {
		httputil.ServiceError(w, err, "set", "template labels")
		return
	}
	if err := h.replaceTemplatePicks(ctx, templateID, picks); err != nil {
		httputil.ServerError(w, "set", "template labels")
		return
	}
	out, err := templatePicks(ctx, h.queries, tenantID, templateID)
	if err != nil {
		httputil.ServerError(w, "list", "template labels")
		return
	}
	httputil.Data(w, out)
}

func (h *Handler) replaceTemplatePicks(ctx context.Context, templateID int64, picks []pick) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	txq := db.New(tx)
	if err := txq.DeleteTemplateLabels(ctx, templateID); err != nil {
		return fmt.Errorf("clear template labels: %w", err)
	}
	for _, p := range picks {
		if err := txq.SetTemplateLabel(ctx, db.SetTemplateLabelParams{
			TemplateID: templateID, LabelDefinitionID: p.definitionID, LabelValueID: p.valueID,
		}); err != nil {
			return fmt.Errorf("set template label: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func templatePicks(ctx context.Context, q db.Querier, tenantID, templateID int64) ([]Pick, error) {
	rows, err := q.ListTemplateLabels(ctx, db.ListTemplateLabelsParams{TemplateID: templateID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list template labels: %w", err)
	}
	out := make([]Pick, 0, len(rows))
	for _, row := range rows {
		out = append(out, Pick{
			LabelDefinitionID: row.LabelDefinitionID, LabelValueID: row.LabelValueID,
			LabelName: row.LabelName, LabelValue: row.LabelValue,
		})
	}
	return out, nil
}
