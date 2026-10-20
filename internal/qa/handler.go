package qa

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/segmentbulk"
)

type Handler struct {
	queries db.Querier
	service *Service
}

// NewHandler wires the QA handler. The pool lets the persisting runs wrap each
// segment's upsert+prune in a transaction; nil degrades to plain writes.
func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, service: NewServiceWithPool(q, pool)}
}

// qaRuleResponse is the API DTO for a QA rule.
//
// Config is json.RawMessage, not the model's `[]byte`: encoding/json would
// base64 a byte slice, and the frontend parses this field as JSON
// (`{"missing": false, "extra": true}`). The runtime path
// (Service.checkSegment → CheckAllWithParams → RuleConfigs) reads the `[]byte`
// straight from the row and never goes through encoding/json.
type qaRuleResponse struct {
	ID        int64           `json:"id"`
	ProfileID int64           `json:"profile_id"`
	CheckType string          `json:"check_type"`
	Enabled   bool            `json:"enabled"`
	Severity  string          `json:"severity"`
	Config    json.RawMessage `json:"config"`
}

func toRuleResponse(r db.QaRule) qaRuleResponse {
	cfg := json.RawMessage(r.Config)
	if len(cfg) == 0 {
		cfg = json.RawMessage(`{}`)
	}
	return qaRuleResponse{
		ID:        r.ID,
		ProfileID: r.ProfileID,
		CheckType: r.CheckType,
		Enabled:   r.Enabled,
		Severity:  r.Severity,
		Config:    cfg,
	}
}

func toRuleResponseList(rules []db.QaRule) []qaRuleResponse {
	out := make([]qaRuleResponse, len(rules))
	for i, r := range rules {
		out[i] = toRuleResponse(r)
	}
	return out
}

// --- QA Profile CRUD ---

func (h *Handler) HandleListProfiles(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	profiles, err := h.queries.ListQAProfiles(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "QA profiles")
		return
	}
	httputil.Data(w, profiles)
}

func (h *Handler) HandleCreateProfile(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	var req ProfileInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	profile, err := CreateProfile(r.Context(), h.queries, tenantID, req)
	if err != nil {
		httputil.ServiceError(w, err, "create", "QA profile")
		return
	}
	httputil.Created(w, profile)
}

func (h *Handler) HandleGetProfile(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	profile, err := h.queries.GetQAProfile(r.Context(), db.GetQAProfileParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "QA profile") {
		return
	}

	// Include rules in the response (use DTO so config serializes as JSON)
	rules, err := h.queries.ListQARules(r.Context(), id)
	if err != nil {
		httputil.ServerError(w, "list", "QA rules")
		return
	}

	httputil.Data(w, map[string]any{
		"profile": profile,
		"rules":   toRuleResponseList(rules),
	})
}

func (h *Handler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	var req ProfileInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	profile, err := UpdateProfile(r.Context(), h.queries, tenantID, id, req)
	if err != nil {
		httputil.ServiceError(w, err, "update", "QA profile")
		return
	}
	httputil.Data(w, profile)
}

// HandleDeleteProfile deletes a profile; 409 while any project or template
// uses it (see DeleteProfile).
func (h *Handler) HandleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := DeleteProfile(r.Context(), h.queries, tenantID, id); err != nil {
		httputil.ServiceError(w, err, "delete", "QA profile")
		return
	}
	httputil.NoContent(w)
}

// --- QA Rules ---

func (h *Handler) HandleListRules(w http.ResponseWriter, r *http.Request) {
	profileID, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}

	// Verify ownership
	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetQAProfile(r.Context(), db.GetQAProfileParams{
		ID: profileID, TenantID: tenantID,
	}); httputil.DBError(w, err, "QA profile") {
		return
	}

	rules, err := h.queries.ListQARules(r.Context(), profileID)
	if err != nil {
		httputil.ServerError(w, "list", "QA rules")
		return
	}
	httputil.Data(w, toRuleResponseList(rules)) // DTO so config serializes as JSON
}

func (h *Handler) HandleUpsertRule(w http.ResponseWriter, r *http.Request) {
	profileID, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	var req RuleInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "check_type is required")
		return
	}
	rule, err := UpsertRule(r.Context(), h.queries, tenantID, profileID, req)
	if err != nil {
		httputil.ServiceError(w, err, "upsert", "QA rule")
		return
	}
	httputil.Data(w, toRuleResponse(rule)) // DTO so config serializes as JSON
}

// --- Run QA ---

// HandleRunQA runs QA checks on a job's translations (file + target_language):
// all of them, or the editor's selection when the body carries
// `translation_ids`. Thin wrapper over Service.RunAndPersistJob (shared with
// the MCP openloc_run_qa tool) and RunAndPersistSelection. Tenant isolation
// lives in the service.
func (h *Handler) HandleRunQA(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	// No body = the whole job.
	var req segmentbulk.Request
	if err := httputil.Decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	var result JobRunResult
	if req.TranslationIDs != nil {
		result, err = h.service.RunAndPersistSelection(r.Context(), jobID, tenantID, req.TranslationIDs)
	} else {
		result, err = h.service.RunAndPersistJob(r.Context(), jobID, tenantID)
	}
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		if errors.Is(err, ErrNoQAProfile) {
			httputil.BadRequest(w, ErrNoQAProfile.Error())
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "job")
			return
		}
		logging.Error(r.Context(), err, "QA run failed", "qa.run_job", logging.JobID(jobID))
		httputil.ServerError(w, "run", "QA check")
		return
	}
	if result.NoRules {
		httputil.Data(w, map[string]any{"message": "no enabled QA rules", "issues": 0})
		return
	}
	httputil.Data(w, map[string]any{"issues": result.Findings, "message": "QA completed"})
}

// --- QA Report ---

// HandleQAReport returns the QA results + per-bucket counts for a job.
// Ignored findings are included in results (flagged) and counted under
// summary.ignored, not under their severity.
//
// Tenant isolation: GetJobTenant is the guard; the follow-up queries run on the
// file_id + target_language it returned, never on the caller's job id.
func (h *Handler) HandleQAReport(w http.ResponseWriter, r *http.Request) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{
		ID: jobID, TenantID: tenantID,
	})
	if httputil.DBError(w, err, "job") {
		return
	}

	results, err := h.queries.ListQAResultsForJob(r.Context(), db.ListQAResultsForJobParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		httputil.ServerError(w, "get", "QA results")
		return
	}

	counts, err := h.queries.CountQAIssuesByJob(r.Context(), db.CountQAIssuesByJobParams{
		FileID:         job.FileID,
		TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		httputil.ServerError(w, "count", "QA issues")
		return
	}

	httputil.Data(w, ReportDTO{
		Summary: summaryFromBuckets(counts),
		Results: jobRowsToDTO(results),
	})
}

// --- Per-segment QA results ---

// HandleTranslationQA returns the stored findings for one segment.
//
// Tenant isolation: GetTranslationTenant is the guard, and it has to be here.
// The route's RequireLinguistTranslationAccess only gates linguists — every
// other role passes straight through — and ListQAResultsForTranslation filters
// by translation_id alone. Without this lookup any owner/admin/PM could read
// another tenant's findings by guessing an id, and the messages carry segment
// fragments plus the name of whoever ignored a finding.
func (h *Handler) HandleTranslationQA(w http.ResponseWriter, r *http.Request) {
	translationID, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTranslationTenant(r.Context(), db.GetTranslationTenantParams{
		ID: translationID, TenantID: tenantID,
	}); httputil.DBError(w, err, "translation") {
		return
	}

	results, err := h.queries.ListQAResultsForTranslation(r.Context(), translationID)
	if err != nil {
		httputil.ServerError(w, "get", "QA results")
		return
	}
	httputil.Data(w, translationRowsToDTO(results))
}

// HandleRunTranslationQA re-runs QA for a single translation against its
// currently-stored target text, persists the fresh results, and returns the
// persisted rows — the SAME shape as HandleTranslationQA so the frontend's
// per-segment merge is identical.
//
// This is the on-save refresh path: the CAT editor calls it
// after every autosave so QA badges for the edited segment stay current without
// a manual full-job run. Tenant isolation is enforced by the service, which
// resolves translation → project scoped to the caller's tenant (404 otherwise).
func (h *Handler) HandleRunTranslationQA(w http.ResponseWriter, r *http.Request) {
	translationID, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.service.RunAndPersistTranslation(r.Context(), translationID, tenantID); err != nil {
		if httputil.DBError(w, err, "translation") {
			return
		}
		return
	}

	results, err := h.queries.ListQAResultsForTranslation(r.Context(), translationID)
	if err != nil {
		httputil.ServerError(w, "get", "QA results")
		return
	}
	httputil.Data(w, translationRowsToDTO(results))
}

// --- Ignore / un-ignore a finding ---
//
// Access scope: any member who can reach the translation (linguists only when
// assigned) — the route group applies RequireLinguistTranslationAccess. The
// service query is tenant-scoped through translation → project, so a foreign
// translation id is a 404.

// HandleIgnoreQAFinding marks the current finding of one check on one segment
// as ignored. The row stays (the UI shows it greyed out); it stops counting and
// stops blocking confirm. Re-ignoring overwrites by/at. No request body.
func (h *Handler) HandleIgnoreQAFinding(w http.ResponseWriter, r *http.Request) {
	h.setIgnored(w, r, true)
}

// HandleUnignoreQAFinding clears the ignore flag on the stored row; the
// finding itself stays.
func (h *Handler) HandleUnignoreQAFinding(w http.ResponseWriter, r *http.Request) {
	h.setIgnored(w, r, false)
}

func (h *Handler) setIgnored(w http.ResponseWriter, r *http.Request, ignored bool) {
	translationID, err := httputil.ParseID(r, "translationID")
	if err != nil {
		httputil.BadRequest(w, "invalid translation ID")
		return
	}
	checkType := chi.URLParam(r, "checkType")
	if checkType == "" {
		httputil.BadRequest(w, "check_type is required")
		return
	}
	rows, err := h.service.SetIgnored(r.Context(), SetIgnoredParams{
		TranslationID: translationID,
		TenantID:      auth.TenantIDFromContext(r.Context()),
		UserID:        auth.UserIDFromContext(r.Context()),
		CheckType:     checkType,
		Ignored:       ignored,
	})
	if err != nil {
		if errors.Is(err, ErrNoQAFinding) {
			httputil.NotFound(w, "QA finding")
			return
		}
		logging.Error(r.Context(), err, "set QA finding ignored failed", "qa.set_ignored", logging.TranslationID(translationID))
		httputil.ServerError(w, "update", "QA finding")
		return
	}
	httputil.Data(w, translationRowsToDTO(rows))
}
