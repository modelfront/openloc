package language

import (
	"net/http"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	langs, err := h.queries.ListLanguages(r.Context())
	if err != nil {
		httputil.ServerError(w, "list", "languages")
		return
	}
	httputil.Data(w, langs)
}
