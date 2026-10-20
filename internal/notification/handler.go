package notification

import (
	"net/http"
	"strconv"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Handler provides HTTP endpoints for notifications and preferences.
type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

// HandleList returns a page of the current user's notifications (see List).
// Reads scope by tenant as well as user.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())

	unreadOnly := r.URL.Query().Get("unread") == "true"
	limit := DefaultPageSize
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= MaxPageSize {
			limit = n
		}
	}
	var cursor int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n > 0 {
			cursor = n
		}
	}
	page, err := List(r.Context(), h.queries, tenantID, userID, unreadOnly, cursor, limit)
	if err != nil {
		httputil.ServerError(w, "list", "notifications")
		return
	}
	httputil.Paginated(w, page.Notifications, page.NextCursor, page.HasMore, -1)
}

// HandleUnreadCount returns the count of unread notifications.
func (h *Handler) HandleUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	count, err := h.queries.CountUnreadNotifications(r.Context(), db.CountUnreadNotificationsParams{
		UserID:   userID,
		TenantID: tenantID,
	})
	if err != nil {
		httputil.ServerError(w, "count", "notifications")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"count": count})
}

// HandleMarkRead marks a single notification as read.
func (h *Handler) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	notifID, err := httputil.ParseID(r, "notificationID")
	if err != nil {
		httputil.BadRequest(w, "invalid notification ID")
		return
	}
	if err := MarkRead(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context()), []int64{notifID}); err != nil {
		httputil.ServerError(w, "mark", "as read")
		return
	}
	httputil.NoContent(w)
}

// HandleMarkAllRead marks all unread notifications as read.
func (h *Handler) HandleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	if err := MarkAllRead(r.Context(), h.queries, auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context())); err != nil {
		httputil.ServerError(w, "mark", "all as read")
		return
	}
	httputil.NoContent(w)
}

// HandleGetPreferences returns the current user's email preferences.
func (h *Handler) HandleGetPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := GetPreferences(r.Context(), h.queries, auth.UserIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "load", "preferences")
		return
	}
	httputil.Data(w, prefs)
}

// HandleUpdatePreferences changes the current user's email preferences. An
// omitted field keeps its stored value.
func (h *Handler) HandleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	var patch PreferencesPatch
	if err := httputil.Decode(r, &patch); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	prefs, err := UpdatePreferences(r.Context(), h.queries, auth.UserIDFromContext(r.Context()), patch)
	if err != nil {
		httputil.ServerError(w, "update", "preferences")
		return
	}
	httputil.Data(w, prefs)
}

// --- Org-level notification rules (managers read, owner/admin write) ---

// HandleGetOrgRules returns the organization's notification rules.
func (h *Handler) HandleGetOrgRules(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	rules, err := h.queries.GetOrgNotificationRules(r.Context(), tenantID)
	if err != nil {
		// No rules row = defaults (all true)
		httputil.Data(w, map[string]any{
			"notify_assignment": true,
			"notify_rejection":  true,
			"notify_completion": true,
			"notify_deadline":   true,
		})
		return
	}
	httputil.Data(w, rules)
}

// HandleUpdateOrgRules upserts organization notification rules.
//
// Same partial-update semantics as HandleUpdatePreferences.
// Each scalar is `*bool`; omitted fields preserve the
// current stored value.
func (h *Handler) HandleUpdateOrgRules(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		NotifyAssignment *bool `json:"notify_assignment"`
		NotifyRejection  *bool `json:"notify_rejection"`
		NotifyCompletion *bool `json:"notify_completion"`
		NotifyDeadline   *bool `json:"notify_deadline"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	// Read current — defaults to all-true when no row exists yet (same default
	// HandleGetOrgRules surfaces).
	current, err := h.queries.GetOrgNotificationRules(r.Context(), tenantID)
	notifyAssignment, notifyRejection, notifyCompletion, notifyDeadline := true, true, true, true
	if err == nil {
		notifyAssignment = current.NotifyAssignment
		notifyRejection = current.NotifyRejection
		notifyCompletion = current.NotifyCompletion
		notifyDeadline = current.NotifyDeadline
	}
	if req.NotifyAssignment != nil {
		notifyAssignment = *req.NotifyAssignment
	}
	if req.NotifyRejection != nil {
		notifyRejection = *req.NotifyRejection
	}
	if req.NotifyCompletion != nil {
		notifyCompletion = *req.NotifyCompletion
	}
	if req.NotifyDeadline != nil {
		notifyDeadline = *req.NotifyDeadline
	}

	rules, err := h.queries.UpsertOrgNotificationRules(r.Context(), db.UpsertOrgNotificationRulesParams{
		TenantID:         tenantID,
		NotifyAssignment: notifyAssignment,
		NotifyRejection:  notifyRejection,
		NotifyCompletion: notifyCompletion,
		NotifyDeadline:   notifyDeadline,
	})
	if err != nil {
		httputil.ServerError(w, "update", "org notification rules")
		return
	}
	httputil.Data(w, rules)
}
