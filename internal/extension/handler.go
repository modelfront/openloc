package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/pkg/extsdk"
)

// Handler serves /api/ai/extensions. Listing is for managers and up;
// uploading and deleting are owner/admin (gated in api.go).
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

type extensionResponse struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Description  string           `json:"description,omitempty"`
	Provider     string           `json:"provider"`
	Capabilities []string         `json:"capabilities"`
	Settings     []extsdk.Setting `json:"settings"`
	AllowedHosts []string         `json:"allowed_hosts"`
	CreatedAt    string           `json:"created_at"`
	UpdatedAt    string           `json:"updated_at"`
}

func responseOf(i Installed) extensionResponse {
	return extensionResponse{
		ID: i.Row.ExtID, Name: i.Manifest.Name, Version: i.Manifest.Version, Description: i.Manifest.Description,
		Provider: i.Provider(), Capabilities: i.Manifest.Capabilities, Settings: i.Manifest.Settings,
		AllowedHosts: i.Manifest.AllowedHosts,
		CreatedAt:    i.Row.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:    i.Row.UpdatedAt.Time.Format(time.RFC3339),
	}
}

// HandleList is GET /api/ai/extensions.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		logging.Error(r.Context(), err, "extension list failed", "extension-list-failed")
		httputil.ServerError(w, "list", "extensions")
		return
	}
	out := make([]extensionResponse, 0, len(list))
	for _, i := range list {
		out = append(out, responseOf(i))
	}
	httputil.Data(w, out)
}

// HandleUpload is POST /api/ai/extensions, a multipart form with the zip in
// "file". An extension with the same id is replaced.
func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxZipBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httputil.BadRequest(w, "file too large or invalid form")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxZipBytes+1))
	if err != nil {
		httputil.BadRequest(w, "could not read the file")
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	inst, err := h.svc.Install(r.Context(), auth.TenantIDFromContext(r.Context()), &userID, data)
	var invalid *InvalidPackageError
	if errors.As(err, &invalid) {
		httputil.FieldError(w, http.StatusBadRequest, "file", invalid.Msg)
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "extension install failed", "extension-install-failed")
		httputil.ServerError(w, "install", "extension")
		return
	}
	logging.Info(r.Context(), "extension installed", "extension-installed",
		logging.Label("extension_id", inst.Row.ExtID), logging.Label("extension_version", inst.Row.Version))
	httputil.Created(w, responseOf(inst))
}

// HandleDelete is DELETE /api/ai/extensions/{extID}. The extension's keys
// and endpoints are deleted with it; engines that used them read as having
// no key.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Delete(r.Context(), auth.TenantIDFromContext(r.Context()), chi.URLParam(r, "extID"))
	if errors.Is(err, ErrNotInstalled) {
		httputil.NotFound(w, "extension")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "extension delete failed", "extension-delete-failed")
		httputil.ServerError(w, "delete", "extension")
		return
	}
	httputil.NoContent(w)
}

// HandleIcon is GET /api/ai/extensions/{extID}/icon, open to every member:
// the icon shows wherever the provider does. The PNG was checked at upload.
func (h *Handler) HandleIcon(w http.ResponseWriter, r *http.Request) {
	icon, err := h.svc.Icon(r.Context(), auth.TenantIDFromContext(r.Context()), chi.URLParam(r, "extID"))
	if errors.Is(err, ErrNoIcon) {
		httputil.NotFound(w, "icon")
		return
	}
	if err != nil {
		logging.Error(r.Context(), err, "extension icon failed", "extension-icon-failed")
		httputil.ServerError(w, "load", "icon")
		return
	}
	sum := sha256.Sum256(icon)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	// Revalidate every time, so a replaced extension shows its new icon.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if _, err := w.Write(icon); err != nil {
		logging.Warn(r.Context(), err, "extension icon write failed", "extension-icon-write-failed")
	}
}
