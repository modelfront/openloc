package tmprofile

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
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

// detailResponse is the GET shape: the list-row columns plus the TM rows.
type detailResponse struct {
	db.GetTMProfileWithStatsRow
	TMAssignments []db.SettingsTmAssignment `json:"tm_assignments"`
}

// writeErr maps a duplicate-name unique violation to 409 and
// anything else to 500; client errors from the service become 400.
func writeErr(w http.ResponseWriter, err error, verb string) {
	if msg, ok := httputil.AsClientError(err); ok {
		httputil.BadRequest(w, msg)
		return
	}
	if httputil.IsUniqueViolation(err) {
		httputil.Error(w, http.StatusConflict, "a TM profile with this name already exists")
		return
	}
	httputil.ServerError(w, verb, "TM profile")
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	rows, err := h.queries.ListTMProfiles(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "TM profiles")
		return
	}
	httputil.Data(w, rows)
}

// detail loads the response shape for one profile, or writes a 404/500.
func (h *Handler) detail(w http.ResponseWriter, ctx context.Context, id, tenantID int64) (detailResponse, bool) {
	row, err := h.queries.GetTMProfileWithStats(ctx, db.GetTMProfileWithStatsParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "TM profile") {
		return detailResponse{}, false
	}
	tms, err := h.queries.ListSettingsTMAssignments(ctx, row.SettingsProfileID)
	if err != nil {
		httputil.ServerError(w, "list", "TM assignments")
		return detailResponse{}, false
	}
	return detailResponse{GetTMProfileWithStatsRow: row, TMAssignments: tms}, true
}

func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM profile ID")
		return
	}
	if resp, ok := h.detail(w, r.Context(), id, auth.TenantIDFromContext(r.Context())); ok {
		httputil.Data(w, resp)
	}
}

// HandleCreate: name + optional description, scoring and initial TM rows, in
// one transaction. 409 on a duplicate name (case-insensitive, per tenant).
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scoring
		TMs []settings.TMAssignmentInput `json:"tms"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	createdBy := auth.UserIDPtr(auth.UserIDFromContext(r.Context()))

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)

	tp, err := Create(r.Context(), q, tenantID, createdBy, req.Name, req.Description, req.Scoring, req.TMs)
	if err != nil {
		writeErr(w, err, "create")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "TM profile")
		return
	}
	if resp, ok := h.detail(w, r.Context(), tp.ID, tenantID); ok {
		httputil.Created(w, resp)
	}
}

// HandleUpdate edits name, description and scoring. Every field is optional;
// absent fields keep their stored values.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM profile ID")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Scoring
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	existing, err := h.queries.GetTMProfile(r.Context(), db.GetTMProfileParams{ID: id, TenantID: tenantID})
	if httputil.DBError(w, err, "TM profile") {
		return
	}
	if !h.guardFollowers(w, r, id, tenantID) {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httputil.ServerError(w, "start", "transaction")
		return
	}
	defer tx.Rollback(r.Context())

	if err := Update(r.Context(), db.New(tx), tenantID, existing, UpdateInput{
		Name: req.Name, Description: req.Description, Scoring: req.Scoring,
	}); err != nil {
		writeErr(w, err, "update")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httputil.ServerError(w, "commit", "TM profile")
		return
	}
	if resp, ok := h.detail(w, r.Context(), id, tenantID); ok {
		httputil.Data(w, resp)
	}
}

// HandleDelete soft-deletes a profile. Refused with 409 while any project or
// template still follows it — those would silently lose their TMs otherwise.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM profile ID")
		return
	}
	err = Delete(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), id)
	var inUse InUseError
	switch {
	case err == nil:
		httputil.NoContent(w)
	case errors.As(err, &inUse):
		httputil.ErrorCode(w, http.StatusConflict, "tm_profile_in_use", inUse.Error())
	case errors.Is(err, pgx.ErrNoRows):
		httputil.NotFound(w, "TM profile")
	default:
		logging.Error(r.Context(), err, "soft-delete TM profile", "tm-profile-delete-failed")
		httputil.ServerError(w, "delete", "TM profile")
	}
}

// HandleSetTMAssignments replaces the profile's TM rows (full replace, in a
// transaction; every tm_id is checked against the tenant).
func (h *Handler) HandleSetTMAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM profile ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	tp, err := h.queries.GetTMProfile(r.Context(), db.GetTMProfileParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.NotFound(w, "TM profile")
			return
		}
		httputil.ServerError(w, "get", "TM profile")
		return
	}
	if !h.guardFollowers(w, r, id, tenantID) {
		return
	}
	rows, err := settings.SetTMAssignmentsTx(r.Context(), h.pool, tenantID, tp.SettingsProfileID, r)
	if err != nil {
		writeErr(w, err, "set")
		return
	}
	httputil.Data(w, rows)
}

// guardFollowers enforces the optional `max_followers` query parameter: the
// edit applies only while at most that many projects + templates follow the
// profile. The settings editor sends it when it edits a followed profile in
// place (0 from the New Project wizard, 1 from a project or template page), so
// a profile that someone else started following between page load and save is
// not changed under them. Writes 409 `tm_profile_shared` and returns false when
// the condition fails or the parameter is malformed.
func (h *Handler) guardFollowers(w http.ResponseWriter, r *http.Request, id, tenantID int64) bool {
	raw := r.URL.Query().Get("max_followers")
	if raw == "" {
		return true
	}
	max, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || max < 0 {
		httputil.BadRequest(w, "invalid max_followers")
		return false
	}
	usage, err := h.queries.CountTMProfileFollowers(r.Context(), db.CountTMProfileFollowersParams{TmProfileID: &id, TenantID: tenantID})
	if err != nil {
		httputil.ServerError(w, "count", "TM profile usage")
		return false
	}
	if int64(usage.ProjectCount+usage.TemplateCount) > max {
		httputil.ErrorCode(w, http.StatusConflict, "tm_profile_shared",
			fmt.Sprintf("TM profile is now followed by %d project(s) and %d template(s); edit it on its own page", usage.ProjectCount, usage.TemplateCount))
		return false
	}
	return true
}
