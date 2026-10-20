// Package project owns the /api/projects HTTP surface.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/projectdelete"
	"openloc/internal/settings"
	"openloc/internal/workflow"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
	// aiEnqueuer runs the QE+APE step for a job whose appended hybrid/ai step
	// was started by a workflow edit. nil skips it.
	aiEnqueuer workflow.AIStepEnqueuer
	// files turns a draft's uploads into jobs at publish. nil skips it.
	files FileMaterializer
	// deleteQueue runs the purge of a deleted project. nil purges in the request.
	deleteQueue projectdelete.TaskEnqueuer
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// SetAIStepEnqueuer wires predict-on-start for steps a workflow edit starts.
func (h *Handler) SetAIStepEnqueuer(e workflow.AIStepEnqueuer) { h.aiEnqueuer = e }

// createRequest is the body of POST /api/projects and of the draft autosave
// PUT /api/projects/{id}/draft — the wizard sends its whole form either way.
//
// Customer_id optional; status optional (defaults in_progress);
// created_by from JWT. Validation + creation live in project.Create
// — shared with the MCP create_project tool, so the two
// surfaces cannot drift.
type createRequest struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	CustomerID      *int64   `json:"customer_id"`
	Status          string   `json:"status"`
	Deadline        string   `json:"deadline"`
	Client          string   `json:"client"`
	Domain          string   `json:"domain"`
	// Optional: apply this template's settings + assignments at creation.
	// Templates are create-time-only — there is no post-create apply. A draft
	// save only re-stamps the provenance; the wizard has already resolved the
	// template into the settings it sends.
	TemplateID *int64 `json:"template_id"`
	// Optional initial configuration, applied atomically with creation so
	// the audit log records ONE project.created event instead of a burst
	// of settings/assignment "changes".
	// Absent lists keep the template values / defaults; present lists are
	// full replaces (the PUT endpoints' semantics).
	Settings *struct {
		FuzzyThreshold *float32 `json:"fuzzy_threshold"`
		MtOnUpload     *bool    `json:"mt_on_upload"`
		TmOnUpload     *bool    `json:"tm_on_upload"`
		AutoComplete   *bool    `json:"auto_complete"`
		InlineHTML     *bool    `json:"inline_html"`
		// Match scoring; nil keeps the template's value or the column default.
		SublanguagePenaltyPct *int32  `json:"sublanguage_penalty_pct"`
		NearExactPenaltyPct   *int32  `json:"near_exact_penalty_pct"`
		TMLookupOrder         *string `json:"tm_lookup_order"`
		// Link the new project to a TM profile (its TMs + match scoring come
		// from the profile). Mutually exclusive with tm_assignments.
		TMProfileID *int64 `json:"tm_profile_id"`
		// Tri-state like PATCH /settings: absent = keep
		// (template value), null/0 = clear, N>0 = set. The wizard sends
		// null when the user cleared a template's prefilled profile.
		QAProfileID json.RawMessage `json:"qa_profile_id"`
		// MT router for the project (nil = keep the template's, if any).
		MTRouterID *int64 `json:"mt_router_id"`
	} `json:"settings"`
	TMAssignments   []settings.TMAssignmentInput   `json:"tm_assignments"`
	TermAssignments []settings.TermAssignmentInput `json:"term_assignments"`
	// Optional library workflow to copy onto the project.
	WorkflowID    *int64               `json:"workflow_id"`
	WorkflowSteps []settings.StepInput `json:"workflow_steps"`
}

// params splits the body into the metadata and the setup halves. The returned
// error is a user-facing 400 message.
func (req *createRequest) params() (CreateParams, SetupConfig, error) {
	params := CreateParams{
		Name:            req.Name,
		Description:     req.Description,
		SourceLanguage:  req.SourceLanguage,
		TargetLanguages: req.TargetLanguages,
		CustomerID:      req.CustomerID,
		Status:          req.Status,
		Deadline:        req.Deadline,
		Client:          req.Client,
		Domain:          req.Domain,
	}
	cfg := SetupConfig{
		TMAssignments:   req.TMAssignments,
		TermAssignments: req.TermAssignments,
		WorkflowID:      req.WorkflowID,
		WorkflowSteps:   req.WorkflowSteps,
	}
	if req.Settings != nil {
		cfg.FuzzyThreshold = req.Settings.FuzzyThreshold
		cfg.MtOnUpload = req.Settings.MtOnUpload
		cfg.TmOnUpload = req.Settings.TmOnUpload
		cfg.AutoComplete = req.Settings.AutoComplete
		cfg.InlineHTML = req.Settings.InlineHTML
		cfg.SublanguagePenaltyPct = req.Settings.SublanguagePenaltyPct
		cfg.NearExactPenaltyPct = req.Settings.NearExactPenaltyPct
		cfg.TMLookupOrder = req.Settings.TMLookupOrder
		if req.Settings.MTRouterID != nil && *req.Settings.MTRouterID > 0 {
			cfg.MTRouterID = req.Settings.MTRouterID
		}
		if req.Settings.TMProfileID != nil && *req.Settings.TMProfileID > 0 {
			cfg.TMProfileID = req.Settings.TMProfileID
		}
		if len(req.Settings.QAProfileID) > 0 {
			if raw := strings.TrimSpace(string(req.Settings.QAProfileID)); raw == "null" {
				cfg.ClearQAProfile = true
			} else {
				var n int64
				if err := json.Unmarshal(req.Settings.QAProfileID, &n); err != nil || n < 0 {
					return params, cfg, errors.New("invalid settings.qa_profile_id (must be null, 0, or positive integer)")
				}
				if n == 0 {
					cfg.ClearQAProfile = true // legacy 0-clears
				} else {
					cfg.QaProfileID = &n
				}
			}
		}
	}
	return params, cfg, nil
}

// templateID is the template to apply, or nil (absent and 0 both mean none).
func (req *createRequest) templateID() *int64 {
	if req.TemplateID != nil && *req.TemplateID != 0 {
		return req.TemplateID
	}
	return nil
}

// decodeCreateRequest reads the body into the shape both POST /api/projects
// and the draft autosave PUT use. It has written the response when ok is false.
func (h *Handler) decodeCreateRequest(w http.ResponseWriter, r *http.Request) (req createRequest, params CreateParams, cfg SetupConfig, ok bool) {
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return req, params, cfg, false
	}
	params, cfg, err := req.params()
	if err != nil {
		httputil.BadRequest(w, err.Error())
		return req, params, cfg, false
	}
	return req, params, cfg, true
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	req, params, cfg, ok := h.decodeCreateRequest(w, r)
	if !ok {
		return
	}
	project, err := CreateWithSetup(r.Context(), h.pool, h.queries, tenantID, userID, params, req.templateID(), cfg)
	if err != nil {
		WriteCreateError(w, r, err)
		return
	}
	httputil.Created(w, project)
}

// CreateInputError reports whether a CreateWithSetup error is the caller's
// input (a 400), and returns the message to show.
func CreateInputError(err error) (string, bool) {
	if errors.Is(err, ErrValidation) {
		// The wrapped message is authored user-facing; strip the sentinel prefix.
		return strings.TrimPrefix(err.Error(), ErrValidation.Error()+": "), true
	}
	return httputil.AsClientError(err)
}

// WriteCreateError writes the response for a failed create: 400 with the
// message for an input problem, 500 otherwise. A failed create leaves no
// project behind, so a retry is safe.
func WriteCreateError(w http.ResponseWriter, r *http.Request, err error) {
	writeCreateError(r.Context(), w, err, "create", "project", "project-create-failed")
}

// writeCreateError writes 400 with the message for an input problem
// (CreateInputError); any other error is logged under action and answered 500,
// described by verb + resource.
func writeCreateError(ctx context.Context, w http.ResponseWriter, err error, verb, resource, action string, attrs ...slog.Attr) {
	if msg, ok := CreateInputError(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	logging.Error(ctx, err, verb+" "+resource+" failed", action, attrs...)
	httputil.ServerError(w, verb, resource)
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "project") {
		return
	}
	httputil.Data(w, project)
}

// Source and target languages are fixed after create: changing them would
// invalidate every existing translation, so no endpoint edits them. Metadata
// edits go through HandleUpdateMetadata (manager-level PATCH).

// HandleDelete soft-deletes a project: every read leaves it out at once, and
// projectdelete purges it and everything it owns on the worker. Without a
// worker queue the purge runs in the request.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "project") {
		return
	}

	// Deleting a project is owner/admin — except a draft, which the
	// manager filling the wizard in may throw away with Discard.
	// The route group only checks manager; this is the
	// rest of the gate. The carve-out is the draft status, not authorship: a
	// draft is unfinished wizard state, and managers manage projects
	// collectively — same rule as deleting a file from a draft.
	draftOnly := !auth.IsAdmin(r)
	if draftOnly && project.Status != db.ProjectStatusDraft {
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	n, err := h.queries.MarkProjectDeleted(r.Context(), db.MarkProjectDeletedParams{
		ID: id, TenantID: tenantID, DraftOnly: draftOnly,
	})
	if httputil.ServiceError(w, err, "delete", "project") {
		return
	}
	if n == 0 {
		// Deleted, or published past the draft gate, since the read above.
		if _, err := h.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, TenantID: tenantID}); httputil.DBError(w, err, "project") {
			return
		}
		httputil.Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	if h.deleteQueue != nil {
		if err := projectdelete.Enqueue(h.deleteQueue, tenantID, id); err != nil {
			// The sweep enqueues it again; the project is already gone for users.
			logging.Error(r.Context(), err, "project purge not queued; the sweep retries it", "project-purge-enqueue-failed",
				logging.ProjectID(id))
		}
	} else if err := projectdelete.New(h.pool, nil).Purge(r.Context(), tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "project")
		return
	}
	httputil.NoContent(w)
}

// SetDeleteQueue makes project purges run on the worker.
func (h *Handler) SetDeleteQueue(q projectdelete.TaskEnqueuer) { h.deleteQueue = q }

// HandleClearTranslations — POST /api/projects/{projectID}/clear-translations
// {scope: "non_confirmed"|"all"}. Rules in ClearTranslations.
func (h *Handler) HandleClearTranslations(w http.ResponseWriter, r *http.Request) {
	projectID, err := httputil.ParseID(r, "projectID")
	if err != nil {
		httputil.BadRequest(w, "invalid project ID")
		return
	}
	var req struct {
		Scope string `json:"scope"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	res, err := h.ClearTranslations(r.Context(), auth.TenantIDFromContext(r.Context()), projectID, ClearScope(req.Scope))
	if err != nil {
		WriteServiceError(w, err, "clear", "translations")
		return
	}
	httputil.JSON(w, http.StatusOK, res)
}
