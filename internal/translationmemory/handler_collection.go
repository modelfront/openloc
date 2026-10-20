package translationmemory

import (
	"errors"
	"net/http"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// TM Collection CRUD — list / create / get / rename / delete.

func (h *Handler) HandleListCollections(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	tms, err := h.queries.ListTMCollections(r.Context(), tenantID)
	if err != nil {
		httputil.ServerError(w, "list", "TMs")
		return
	}
	httputil.Data(w, tms)
}

// HandleCreateCollection: a source language, one or more target languages,
// and an optional customer (absent/null/0 = none).
func (h *Handler) HandleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string   `json:"name"`
		SourceLanguage  string   `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
		CustomerID      *int64   `json:"customer_id"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	tm, err := h.CreateCollection(r.Context(), auth.TenantIDFromContext(r.Context()), auth.UserIDFromContext(r.Context()),
		CreateCollectionInput{
			Name: req.Name, SourceLanguage: req.SourceLanguage,
			TargetLanguages: req.TargetLanguages, CustomerID: req.CustomerID,
		})
	var verr ValidationError
	if errors.As(err, &verr) {
		httputil.BadRequest(w, verr.Msg)
		return
	}
	if err != nil {
		httputil.ServerError(w, "create", "TM")
		return
	}
	httputil.Created(w, tm)
}

func (h *Handler) HandleGetCollection(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	tm, err := h.queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "TM") {
		return
	}
	httputil.Data(w, tm)
}

func (h *Handler) HandleRenameCollection(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())

	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}

	tm, err := h.queries.RenameTMCollection(r.Context(), db.RenameTMCollectionParams{
		ID:       id,
		Name:     req.Name,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "TM") {
		return
	}
	httputil.Data(w, tm)
}

// HandleDeleteCollection soft-deletes the TM; 409 while anything uses it.
func (h *Handler) HandleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}
	err = h.DeleteCollection(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	var inUse InUseError
	switch {
	case err == nil:
		httputil.NoContent(w)
	case errors.As(err, &inUse):
		httputil.Error(w, http.StatusConflict, inUse.Error())
	default:
		httputil.DBError(w, err, "TM")
	}
}
