package project

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/settings"
)

// The HTTP side of the project settings surface: request parsing and error
// mapping. The rules live in settings_service.go, shared with MCP.

// ProjectSettings merges project fields with its settings profile scalars —
// the JSON shape the frontend reads.
type ProjectSettings struct {
	db.Project
	FuzzyThreshold        float32 `json:"fuzzy_threshold"`
	MtOnUpload            bool    `json:"mt_on_upload"`
	TmOnUpload            bool    `json:"tm_on_upload"`
	QaProfileID           *int64  `json:"qa_profile_id"`
	MTRouterID            *int64  `json:"mt_router_id"`
	MTRouterName          *string `json:"mt_router_name"`
	SublanguagePenaltyPct int32   `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   int32   `json:"near_exact_penalty_pct"`
	TMLookupOrder         string  `json:"tm_lookup_order"`
	AutoComplete          bool    `json:"auto_complete"`
	InlineHTML            bool    `json:"inline_html"`
	// Library workflow the steps were copied from; nil
	// for an inline workflow. The name resolves even for a retired library row.
	WorkflowID   *int64  `json:"workflow_id"`
	WorkflowName *string `json:"workflow_name"`
}

// HandleUpdateSettings — PATCH /api/projects/{projectID}/settings.
//
// qa_profile_id and mt_router_id are tri-state: absent keeps, null or 0
// clears, N sets (*int64 alone cannot tell absent from null, hence
// json.RawMessage). tm_profile_id links; null is refused — leaving a profile
// goes through the detach endpoint.
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	var req struct {
		FuzzyThreshold        *float32        `json:"fuzzy_threshold"`
		MtOnUpload            *bool           `json:"mt_on_upload"`
		TmOnUpload            *bool           `json:"tm_on_upload"`
		QAProfileID           json.RawMessage `json:"qa_profile_id"`
		SublanguagePenaltyPct *int32          `json:"sublanguage_penalty_pct"`
		NearExactPenaltyPct   *int32          `json:"near_exact_penalty_pct"`
		AutoComplete          *bool           `json:"auto_complete"`
		InlineHTML            *bool           `json:"inline_html"`
		TMLookupOrder         *string         `json:"tm_lookup_order"`
		TMProfileID           json.RawMessage `json:"tm_profile_id"`
		MTRouterID            json.RawMessage `json:"mt_router_id"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	patch := SettingsPatch{
		FuzzyThreshold:        req.FuzzyThreshold,
		MtOnUpload:            req.MtOnUpload,
		TmOnUpload:            req.TmOnUpload,
		SublanguagePenaltyPct: req.SublanguagePenaltyPct,
		NearExactPenaltyPct:   req.NearExactPenaltyPct,
		AutoComplete:          req.AutoComplete,
		InlineHTML:            req.InlineHTML,
		TMLookupOrder:         req.TMLookupOrder,
	}
	if len(req.QAProfileID) > 0 {
		var n int64
		if strings.TrimSpace(string(req.QAProfileID)) != "null" {
			if err := json.Unmarshal(req.QAProfileID, &n); err != nil {
				httputil.BadRequest(w, "invalid qa_profile_id (must be null or integer)")
				return
			}
		}
		patch.QAProfileID = &n
	}
	linkTo, _, err := settings.ParseTMProfileIDPatch(req.TMProfileID)
	if err != nil {
		WriteServiceError(w, err, "update", "project settings")
		return
	}
	patch.TMProfileID = linkTo
	mtRouter, setMTRouter, err := settings.ParseNullableIDPatch(req.MTRouterID, "mt_router_id")
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if setMTRouter {
		var n int64
		if mtRouter != nil {
			n = *mtRouter
		}
		patch.MTRouterID = &n
	}

	out, err := h.UpdateSettings(r.Context(), auth.TenantIDFromContext(r.Context()), id, patch)
	if err != nil {
		WriteServiceError(w, err, "update", "project settings")
		return
	}
	httputil.Data(w, out)
}

// WriteServiceError maps a project service error: an unknown project is 404,
// a TM-profile-linked edit the 409 with the code the UI keys on, the rest as
// httputil.ServiceError.
func WriteServiceError(w http.ResponseWriter, err error, verb, resource string) {
	if errors.Is(err, settings.ErrTMProfileLinked) {
		settings.WriteTMProfileLinked(w)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httputil.NotFound(w, "project")
		return
	}
	httputil.ServiceError(w, err, verb, resource)
}

func (h *Handler) newProjectSettings(ctx context.Context, tenantID int64, project db.Project, p db.SettingsProfile) ProjectSettings {
	return ProjectSettings{
		Project:               project,
		FuzzyThreshold:        p.FuzzyThreshold,
		MtOnUpload:            p.MtOnUpload,
		TmOnUpload:            p.TmOnUpload,
		QaProfileID:           p.QaProfileID,
		SublanguagePenaltyPct: p.SublanguagePenaltyPct,
		NearExactPenaltyPct:   p.NearExactPenaltyPct,
		AutoComplete:          p.AutoComplete,
		InlineHTML:            p.InlineHtml,
		TMLookupOrder:         p.TmLookupOrder,
		WorkflowID:            p.WorkflowID,
		WorkflowName:          settings.WorkflowName(ctx, h.queries, tenantID, p.WorkflowID),
		MTRouterID:            p.MtRouterID,
		MTRouterName:          settings.MTRouterName(ctx, h.queries, tenantID, p.MtRouterID),
	}
}

// mtRouterName resolves a router id from a settings diff to its display
// name, including retired routers; nil when unset or unresolvable.
func mtRouterName(ctx context.Context, q db.Querier, tenantID int64, id *int64) any {
	if name := settings.MTRouterName(ctx, q, tenantID, id); name != nil {
		return *name
	}
	return nil
}

// HandleGetSettings returns project settings + all assignment tables.
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	out, err := h.Settings(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if err != nil {
		WriteServiceError(w, err, "get", "project settings")
		return
	}
	httputil.Data(w, out)
}

// resolveProjectProfile parses the project ID, verifies tenant ownership,
// and returns the project row (callers use SettingsProfileID; the audit
// emissions also need ID/TenantID).
func (h *Handler) resolveProjectProfile(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return db.Project{}, false
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "project")
			return db.Project{}, false
		}
		httputil.ServerError(w, "verify", "project")
		return db.Project{}, false
	}
	return project, true
}

// HandleSetTMAssignments — PUT /api/projects/{projectID}/tms {assignments}.
func (h *Handler) HandleSetTMAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		Assignments []settings.TMAssignmentInput `json:"assignments"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	out, err := h.SetTMAssignments(r.Context(), auth.TenantIDFromContext(r.Context()), id, req.Assignments)
	if err != nil {
		WriteServiceError(w, err, "set", "TM assignments")
		return
	}
	httputil.Data(w, out)
}

// HandleSetTermAssignments — PUT /api/projects/{projectID}/terminology {assignments}.
func (h *Handler) HandleSetTermAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		Assignments []settings.TermAssignmentInput `json:"assignments"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	out, err := h.SetTermAssignments(r.Context(), auth.TenantIDFromContext(r.Context()), id, req.Assignments)
	if err != nil {
		WriteServiceError(w, err, "set", "terminology assignments")
		return
	}
	httputil.Data(w, out)
}
