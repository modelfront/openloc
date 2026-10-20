package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/extension"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/pkg/extsdk"
)

// Extensions is the tenant's installed extensions, as AI vendors
// (implemented by *extension.Service).
type Extensions interface {
	List(ctx context.Context, tenantID int64) ([]extension.Installed, error)
	GetByProvider(ctx context.Context, tenantID int64, provider string) (extension.Installed, error)
	CheckKey(ctx context.Context, tenantID int64, provider, token, baseURL string) error
}

// WithExtensions lists installed extensions as vendors and checks their keys
// with the extension's check_key. Returns h for chaining.
func (h *Handler) WithExtensions(e Extensions) *Handler {
	h.ext = e
	return h
}

// extensionInfo describes an extension-backed vendor to the client.
type extensionInfo struct {
	ID           string           `json:"id"`
	Version      string           `json:"version"`
	Capabilities []string         `json:"capabilities"`
	Settings     []extsdk.Setting `json:"settings"`
	AllowedHosts []string         `json:"allowed_hosts"`
}

func extensionInfoOf(inst extension.Installed) *extensionInfo {
	return &extensionInfo{
		ID: inst.Row.ExtID, Version: inst.Row.Version, Capabilities: inst.Manifest.Capabilities,
		Settings: inst.Manifest.Settings, AllowedHosts: inst.Manifest.AllowedHosts,
	}
}

// installedExtension returns the extension behind an "ext:" provider. ok is
// false when the provider is not one or is not installed; err is a lookup
// failure, which is neither.
func (h *Handler) installedExtension(r *http.Request, provider string) (inst extension.Installed, ok bool, err error) {
	if h.ext == nil {
		return extension.Installed{}, false, nil
	}
	if _, isExt := extension.IDFromProvider(provider); !isExt {
		return extension.Installed{}, false, nil
	}
	inst, err = h.ext.GetByProvider(r.Context(), auth.TenantIDFromContext(r.Context()), provider)
	if errors.Is(err, extension.ErrNotInstalled) {
		return extension.Installed{}, false, nil
	}
	if err != nil {
		return extension.Installed{}, false, err
	}
	return inst, true, nil
}

// requireProvider accepts a built-in vendor or an installed extension. It
// answers 400 for an unknown provider and 500 for a failed lookup, and
// reports whether the caller may go on.
func (h *Handler) requireProvider(w http.ResponseWriter, r *http.Request, provider string) bool {
	if ValidProvider(provider) {
		return true
	}
	_, ok, err := h.installedExtension(r, provider)
	if err != nil {
		logging.Error(r.Context(), err, "extension lookup failed", "ai-extension-lookup-failed", logging.Provider(provider))
		httputil.ServerError(w, "look up", "provider")
		return false
	}
	if !ok {
		httputil.BadRequest(w, "unknown provider")
		return false
	}
	return true
}

// displayName is the vendor name users see; for an extension, its manifest
// name.
func (h *Handler) displayName(r *http.Request, provider string) string {
	if inst, ok, _ := h.installedExtension(r, provider); ok {
		return inst.Manifest.Name
	}
	return DisplayName(provider)
}

// testKey checks a key live: built-in vendors through the tester, an
// extension through its check_key.
func (h *Handler) testKey(r *http.Request, provider, apiKey, baseURL string) error {
	if _, ok := extension.IDFromProvider(provider); ok && h.ext != nil {
		return h.ext.CheckKey(r.Context(), auth.TenantIDFromContext(r.Context()), provider, apiKey, baseURL)
	}
	return h.tester.Test(r.Context(), provider, apiKey, baseURL)
}

// extensionKeyReason is the message for a failed extension key check.
func extensionKeyReason(name string, err error) string {
	var ce *extension.CallError
	if errors.As(err, &ce) {
		return fmt.Sprintf("%s did not accept this key: %s", name, ce.Msg)
	}
	return fmt.Sprintf("Could not check this key with %s. Check the endpoint address and try again.", name)
}
