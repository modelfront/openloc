package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/settings"
)

type Handler struct {
	queries db.Querier
	pool    *pgxpool.Pool
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{queries: q, pool: pool}
}

// templateResponse merges template fields with its settings profile scalars
// so the frontend sees the same JSON shape it expects.
type templateResponse struct {
	db.Template
	FuzzyThreshold        float32 `json:"fuzzy_threshold"`
	MtOnUpload            bool    `json:"mt_on_upload"`
	TmOnUpload            bool    `json:"tm_on_upload"`
	QaProfileID           *int64  `json:"qa_profile_id"`
	SublanguagePenaltyPct int32   `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   int32   `json:"near_exact_penalty_pct"`
	TMLookupOrder         string  `json:"tm_lookup_order"`
	AutoComplete          bool    `json:"auto_complete"`
	InlineHTML            bool    `json:"inline_html"`
	WorkflowID            *int64  `json:"workflow_id"`
	WorkflowName          *string `json:"workflow_name"`
	MTRouterID            *int64  `json:"mt_router_id"`
	MTRouterName          *string `json:"mt_router_name"`
}

// newTemplateResponse builds the wire form of a template. Build responses
// through this, never with a bare struct literal, so every response carries
// every profile field.
func (h *Handler) newTemplateResponse(ctx context.Context, tmpl db.Template, profile db.SettingsProfile) templateResponse {
	return templateResponse{
		Template:              tmpl,
		FuzzyThreshold:        profile.FuzzyThreshold,
		MtOnUpload:            profile.MtOnUpload,
		TmOnUpload:            profile.TmOnUpload,
		QaProfileID:           profile.QaProfileID,
		SublanguagePenaltyPct: profile.SublanguagePenaltyPct,
		NearExactPenaltyPct:   profile.NearExactPenaltyPct,
		AutoComplete:          profile.AutoComplete,
		InlineHTML:            profile.InlineHtml,
		TMLookupOrder:         profile.TmLookupOrder,
		WorkflowID:            profile.WorkflowID,
		WorkflowName:          settings.WorkflowName(ctx, h.queries, tmpl.TenantID, profile.WorkflowID),
		MTRouterID:            profile.MtRouterID,
		MTRouterName:          settings.MTRouterName(ctx, h.queries, tmpl.TenantID, profile.MtRouterID),
	}
}

// The picker pages through templates, but older callers ask for the whole list
// in one request and read only `data`. A limit this high is "no pagination" for
// any realistic tenant, so both keep working — same arrangement as the project
// list (internal/project/handler_metadata.go).
const templateListMaxLimit int32 = 10000

// HandleList returns the tenant's templates, newest first: cursor pagination
// via `cursor` (id of the last item of the previous page, 0 for the first) and
// `limit`, with an optional `q` name search.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	page := httputil.ParseCursor(r, templateListMaxLimit)
	if r.URL.Query().Get("limit") == "" {
		page.Limit = templateListMaxLimit
	}

	params := db.ListTemplatesFilteredParams{
		TenantID: tenantID,
		Cursor:   page.Cursor,
		Lim:      page.Limit + 1, // +1 to detect has_more
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params.Q = &q
	}

	templates, err := h.queries.ListTemplatesFiltered(r.Context(), params)
	if err != nil {
		httputil.ServerError(w, "list", "templates")
		return
	}

	hasMore := int32(len(templates)) > page.Limit
	if hasMore {
		templates = templates[:page.Limit]
	}
	var nextCursor int64
	if hasMore && len(templates) > 0 {
		nextCursor = templates[len(templates)-1].ID
	}
	httputil.Paginated(w, templates, nextCursor, hasMore, -1)
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	tmpl, err := h.queries.GetTemplate(r.Context(), db.GetTemplateParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "template") {
		return
	}

	profile, err := h.queries.GetSettingsProfile(r.Context(), tmpl.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "get", "settings profile")
		return
	}

	// Effective TM rows + scoring (the linked TM profile's when there is one).
	scope, err := settings.ResolveTMScope(r.Context(), h.queries, tenantID, profile)
	if err != nil {
		httputil.ServerError(w, "resolve", "TM settings")
		return
	}

	termAssignments, err := h.queries.ListSettingsTermAssignments(r.Context(), tmpl.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "list", "terminology assignments")
		return
	}

	httputil.Data(w, map[string]any{
		"template":         h.newTemplateResponse(r.Context(), tmpl, scope.Effective(profile)),
		"tm_assignments":   scope.TMAssignments,
		"tm_profile":       scope.Linked,
		"term_assignments": termAssignments,
	})
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		Name           string   `json:"name"`
		MtOnUpload     *bool    `json:"mt_on_upload"`
		TmOnUpload     *bool    `json:"tm_on_upload"`
		FuzzyThreshold *float32 `json:"fuzzy_threshold"` // nil = default 0.75
		// Optional library workflow to copy onto the template.
		WorkflowID *int64 `json:"workflow_id"`
		// Project name, languages, customer and domain a project made from the
		// template starts with.
		ProjectName     *string  `json:"project_name"`
		SourceLanguage  *string  `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
		CustomerID      *int64   `json:"customer_id"`
		Domain          *string  `json:"domain"`
		// The same initial configuration POST /api/projects accepts, so the New
		// project form can be saved as a template in one request. Absent lists
		// leave the template empty; present lists are full replaces.
		Settings *struct {
			FuzzyThreshold        *float32        `json:"fuzzy_threshold"`
			MtOnUpload            *bool           `json:"mt_on_upload"`
			TmOnUpload            *bool           `json:"tm_on_upload"`
			AutoComplete          *bool           `json:"auto_complete"`
			InlineHTML            *bool           `json:"inline_html"`
			SublanguagePenaltyPct *int32          `json:"sublanguage_penalty_pct"`
			NearExactPenaltyPct   *int32          `json:"near_exact_penalty_pct"`
			TMLookupOrder         *string         `json:"tm_lookup_order"`
			TMProfileID           *int64          `json:"tm_profile_id"`
			QAProfileID           json.RawMessage `json:"qa_profile_id"`
			MTRouterID            *int64          `json:"mt_router_id"`
		} `json:"settings"`
		TMAssignments   []settings.TMAssignmentInput   `json:"tm_assignments"`
		TermAssignments []settings.TermAssignmentInput `json:"term_assignments"`
		WorkflowSteps   []settings.StepInput           `json:"workflow_steps"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "name is required")
		return
	}
	cfg := settings.Setup{
		TMAssignments: req.TMAssignments, TermAssignments: req.TermAssignments,
		WorkflowID: req.WorkflowID, WorkflowSteps: req.WorkflowSteps,
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
		if req.Settings.TMProfileID != nil && *req.Settings.TMProfileID > 0 {
			cfg.TMProfileID = req.Settings.TMProfileID
		}
		if req.Settings.MTRouterID != nil && *req.Settings.MTRouterID > 0 {
			cfg.MTRouterID = req.Settings.MTRouterID
		}
		qa, err := parseQAProfileID(req.Settings.QAProfileID, "settings.qa_profile_id")
		if err != nil {
			httputil.BadRequest(w, err.Error())
			return
		}
		if qa.Set {
			cfg.QaProfileID = qa.ID
			cfg.ClearQAProfile = qa.ID == nil
		}
	}
	tmpl, profile, err := Create(r.Context(), h.queries, h.pool, tenantID, CreateInput{
		Name: req.Name, MtOnUpload: req.MtOnUpload, TmOnUpload: req.TmOnUpload,
		FuzzyThreshold: req.FuzzyThreshold, ProjectName: req.ProjectName,
		SourceLanguage: req.SourceLanguage, TargetLanguages: req.TargetLanguages,
		CustomerID: req.CustomerID, Domain: req.Domain, Setup: cfg,
	})
	if err != nil {
		h.writeError(w, r, err, "create")
		return
	}
	h.respondTemplate(w, r, tmpl, profile, http.StatusCreated)
}

// parseQAProfileID reads a tri-state qa_profile_id field: absent keeps, null
// or 0 clears (0 is the legacy spelling), a positive id sets.
func parseQAProfileID(raw json.RawMessage, field string) (NullableID, error) {
	if len(raw) == 0 {
		return NullableID{}, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return NullableID{Set: true}, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
		return NullableID{}, fmt.Errorf("invalid %s (must be null, 0, or positive integer)", field)
	}
	if n == 0 {
		return NullableID{Set: true}, nil
	}
	return NullableID{Set: true, ID: &n}, nil
}

// writeError maps a service error onto the response; a TM scoring edit on a
// linked template gets the coded 409 the UI keys on.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error, verb string) {
	if errors.Is(err, settings.ErrTMProfileLinked) {
		settings.WriteTMProfileLinked(w)
		return
	}
	if _, isClient := httputil.AsClientError(err); !isClient && !errors.Is(err, pgx.ErrNoRows) {
		logging.Error(r.Context(), err, verb+" template", "template-"+verb+"-failed")
	}
	httputil.ServiceError(w, err, verb, "template")
}

// respondTemplate writes a template with its effective settings.
func (h *Handler) respondTemplate(w http.ResponseWriter, r *http.Request, tmpl db.Template, profile db.SettingsProfile, status int) {
	scope, err := settings.ResolveTMScope(r.Context(), h.queries, tmpl.TenantID, profile)
	if err != nil {
		httputil.ServerError(w, "resolve", "TM settings")
		return
	}
	httputil.JSON(w, status, map[string]any{"data": h.newTemplateResponse(r.Context(), tmpl, scope.Effective(profile))})
}

// trimText stores free text trimmed, and an empty value as NULL.
func trimText(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

var trimDomain = trimText

// normalizeLanguages applies the project rules to a template's optional
// languages: codes normalized, empty source → NULL, targets deduplicated,
// a target may not equal the source. nil targets means "none".
func normalizeLanguages(source *string, targets []string) (*string, []string, error) {
	src := ""
	if source != nil {
		src = langcode.Normalize(*source)
	}
	out := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for _, t := range langcode.NormalizeAll(targets) {
		if t == "" || seen[t] {
			continue
		}
		if src != "" && t == src {
			return nil, nil, fmt.Errorf("target language %q cannot be the same as source language", t)
		}
		seen[t] = true
		out = append(out, t)
	}
	if src == "" {
		return nil, out, nil
	}
	return &src, out, nil
}

// HandleUpdate applies a partial edit (see Update). Every field is tri-state:
// absent keeps the stored value. `mt_on_upload` is a *bool so a partial PATCH
// cannot flip it to false, and `qa_profile_id` / `mt_router_id` /
// `customer_id` are raw JSON so absent, null and an id are told apart.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		Name       *string `json:"name"`
		MtOnUpload *bool   `json:"mt_on_upload"`
		TmOnUpload *bool   `json:"tm_on_upload"`
		// Absent leaves the stored value.
		AutoComplete *bool           `json:"auto_complete"`
		InlineHTML   *bool           `json:"inline_html"`
		MTRouterID   json.RawMessage `json:"mt_router_id"`
		// TM match penalties — absent leaves the stored value.
		SublanguagePenaltyPct *int32 `json:"sublanguage_penalty_pct"`
		NearExactPenaltyPct   *int32 `json:"near_exact_penalty_pct"`
		// Absent leaves the stored value.
		TMLookupOrder  *string         `json:"tm_lookup_order"`
		FuzzyThreshold *float32        `json:"fuzzy_threshold"`
		QAProfileID    json.RawMessage `json:"qa_profile_id"`
		// Link to a TM profile; `null` is refused (use the detach endpoint).
		TMProfileID     json.RawMessage `json:"tm_profile_id"`
		CustomerID      json.RawMessage `json:"customer_id"`
		ProjectName     *string         `json:"project_name"`
		Domain          *string         `json:"domain"`
		SourceLanguage  *string         `json:"source_language"`
		TargetLanguages []string        `json:"target_languages"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	in := UpdateInput{
		Name: req.Name, MtOnUpload: req.MtOnUpload, TmOnUpload: req.TmOnUpload,
		AutoComplete: req.AutoComplete, InlineHTML: req.InlineHTML, FuzzyThreshold: req.FuzzyThreshold,
		SublanguagePenaltyPct: req.SublanguagePenaltyPct, NearExactPenaltyPct: req.NearExactPenaltyPct,
		TMLookupOrder: req.TMLookupOrder,
		ProjectName:   req.ProjectName, Domain: req.Domain,
		SourceLanguage: req.SourceLanguage, TargetLanguages: req.TargetLanguages,
	}
	if len(req.CustomerID) > 0 {
		in.Customer.Set = true
		if strings.TrimSpace(string(req.CustomerID)) != "null" {
			var n int64
			if err := json.Unmarshal(req.CustomerID, &n); err != nil {
				httputil.BadRequest(w, "invalid customer_id (must be null or integer)")
				return
			}
			in.Customer.ID = &n
		}
	}
	if in.QAProfile, err = parseQAProfileID(req.QAProfileID, "qa_profile_id"); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if in.MTRouter.ID, in.MTRouter.Set, err = settings.ParseNullableIDPatch(req.MTRouterID, "mt_router_id"); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	if in.LinkTMProfile, _, err = settings.ParseTMProfileIDPatch(req.TMProfileID); err != nil {
		httputil.BadRequest(w, err.Error())
		return
	}
	tmpl, profile, err := Update(r.Context(), h.queries, h.pool, tenantID, id, in)
	if err != nil {
		h.writeError(w, r, err, "update")
		return
	}
	h.respondTemplate(w, r, tmpl, profile, http.StatusOK)
}

// HandleDelete deletes a template and its settings profile (see Delete).
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := Delete(r.Context(), h.queries, h.pool, tenantID, id); err != nil {
		h.writeError(w, r, err, "delete")
		return
	}
	httputil.NoContent(w)
}

// resolveTemplateProfile parses the template ID, verifies tenant ownership,
// and returns the settings profile ID.
func (h *Handler) resolveTemplateProfile(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return 0, false
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	tmpl, err := h.queries.GetTemplate(r.Context(), db.GetTemplateParams{
		ID: id, TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "template")
			return 0, false
		}
		httputil.ServerError(w, "verify", "template")
		return 0, false
	}
	return tmpl.SettingsProfileID, true
}

// HandleSetTMAssignments replaces all TM assignments for a template.
// Wrapped in a DB transaction: any mid-loop failure rolls back the preceding DELETE.
func (h *Handler) HandleSetTMAssignments(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	if own, err := h.queries.GetSettingsProfile(r.Context(), profileID); err != nil {
		httputil.ServerError(w, "get", "settings profile")
		return
	} else if own.TmProfileID != nil {
		settings.WriteTMProfileLinked(w)
		return
	}
	assignments, err := settings.SetTMAssignmentsTx(r.Context(), h.pool, auth.TenantIDFromContext(r.Context()), profileID, r)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServerError(w, "set", "TM assignments")
		return
	}
	httputil.Data(w, assignments)
}

// HandleSetTermAssignments replaces all terminology assignments for a template.
// Wrapped in a DB transaction: any mid-loop failure rolls back the preceding DELETE.
func (h *Handler) HandleSetTermAssignments(w http.ResponseWriter, r *http.Request) {
	profileID, ok := h.resolveTemplateProfile(w, r)
	if !ok {
		return
	}
	assignments, err := settings.SetTermAssignmentsTx(r.Context(), h.pool, auth.TenantIDFromContext(r.Context()), profileID, r)
	if err != nil {
		if msg, ok := httputil.AsClientError(err); ok {
			httputil.BadRequest(w, msg)
			return
		}
		httputil.ServerError(w, "set", "terminology assignments")
		return
	}
	httputil.Data(w, assignments)
}
