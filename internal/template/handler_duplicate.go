package template

import (
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// HandleDuplicate clones a template (see Duplicate).
// POST /api/templates/{templateID}/duplicate
func (h *Handler) HandleDuplicate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "templateID")
	if err != nil {
		httputil.BadRequest(w, "invalid template ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	dup, profile, err := Duplicate(r.Context(), h.queries, h.pool, tenantID, id)
	if err != nil {
		h.writeError(w, r, err, "duplicate")
		return
	}
	resp, ok := h.templateResponseOr500(w, r, dup, profile)
	if !ok {
		return
	}
	httputil.Created(w, resp)
}
