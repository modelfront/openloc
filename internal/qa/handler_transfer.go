package qa

import (
	"net/http"

	"openloc/internal/auth"
	"openloc/internal/httputil"
)

// Access scope for the handlers below: owner, admin and project manager, like
// every other QA profile route (RequireManager on the route group).

// HandleDeleteRule deletes a regex rule of a profile.
func (h *Handler) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	profileID, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	ruleID, err := httputil.ParseID(r, "ruleID")
	if err != nil {
		httputil.BadRequest(w, "invalid rule ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	if err := DeleteRule(r.Context(), h.queries, tenantID, profileID, ruleID); err != nil {
		httputil.ServiceError(w, err, "delete", "QA rule")
		return
	}
	httputil.NoContent(w)
}

// HandleExportProfile downloads a profile and its rules as a JSON file.
func (h *Handler) HandleExportProfile(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	f, err := ExportProfile(r.Context(), h.queries, tenantID, id)
	if err != nil {
		httputil.ServiceError(w, err, "export", "QA profile")
		return
	}
	body, err := EncodeProfileFile(f)
	if err != nil {
		httputil.ServerError(w, "export", "QA profile")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	httputil.SetAttachmentFilename(w, f.Name+".qa-profile.json")
	_, _ = w.Write(body) // the client has gone; nothing left to report to
}

// HandleImportProfile creates a new profile from an exported file (the
// request body).
func (h *Handler) HandleImportProfile(w http.ResponseWriter, r *http.Request) {
	var f ProfileFile
	if err := httputil.Decode(r, &f); err != nil {
		httputil.BadRequest(w, "invalid QA profile file")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	profile, err := ImportProfile(r.Context(), h.queries, h.service.pool, tenantID, f)
	if err != nil {
		httputil.ServiceError(w, err, "import", "QA profile")
		return
	}
	httputil.Created(w, profile)
}

// HandleReplaceProfileRules replaces every rule of a profile with the rules of
// an exported file (the request body).
func (h *Handler) HandleReplaceProfileRules(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid profile ID")
		return
	}
	var f ProfileFile
	if err := httputil.Decode(r, &f); err != nil {
		httputil.BadRequest(w, "invalid QA profile file")
		return
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	rules, err := ReplaceProfileRules(r.Context(), h.queries, h.service.pool, tenantID, id, f)
	if err != nil {
		httputil.ServiceError(w, err, "import", "QA profile")
		return
	}
	httputil.Data(w, toRuleResponseList(rules))
}
