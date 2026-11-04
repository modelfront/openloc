package publicapi

import (
	"net/http"
	"time"

	"openloc/internal/auth"
	"openloc/internal/filterprofile"
	"openloc/internal/httputil"
)

// FilterProfile is a v1 filter profile: a read-only built-in or one of the
// organization's own, for one file format.
type FilterProfile struct {
	ID          int64                 `json:"id"`
	Name        string                `json:"name"`
	BuiltinKey  *string               `json:"builtin_key"`
	Format      string                `json:"format"`
	Builtin     bool                  `json:"builtin"`
	OrgDefault  bool                  `json:"org_default"`
	Description string                `json:"description"`
	BasedOnID   *int64                `json:"based_on_id"`
	Options     filterprofile.Options `json:"options"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
}

// filterProfileInput is the body of a create or a replace.
type filterProfileInput struct {
	Name      string                 `json:"name"`
	Format    string                 `json:"format"`
	Options   *filterprofile.Options `json:"options"`
	FromID    *int64                 `json:"from_id"`
	UpdatedAt *time.Time             `json:"updated_at"`
}

func (in filterProfileInput) service() filterprofile.Input {
	return filterprofile.Input{Name: in.Name, Format: in.Format, Options: in.Options, From: in.FromID, UpdatedAt: in.UpdatedAt}
}

func filterProfileDTO(p filterprofile.Profile) FilterProfile {
	out := FilterProfile{
		ID: p.ID, Name: p.Name, Format: p.Format, Builtin: p.Builtin, OrgDefault: p.OrgDefault, Description: p.Description, BasedOnID: p.BasedOn, Options: p.Options,
		// Fractional seconds: a replace sends updated_at back, and it must
		// match the stored value exactly.
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if p.BuiltinKey != "" {
		out.BuiltinKey = &p.BuiltinKey
	}
	return out
}

func (h Handlers) filterProfiles() *filterprofile.Service {
	return filterprofile.NewService(h.Queries, h.Pool)
}

func (h Handlers) listFilterProfiles(w http.ResponseWriter, r *http.Request) {
	page := httputil.ParseCursor(r, listMaxLimit)
	profiles, err := h.filterProfiles().ListPage(r.Context(), auth.TenantIDFromContext(r.Context()),
		r.URL.Query().Get("format"), page.Cursor, page.Limit+1)
	if httputil.ServiceError(w, err, "list", "filter profiles") {
		return
	}
	writePage(w, profiles, page.Limit, func(p filterprofile.Profile) int64 { return p.ID }, filterProfileDTO)
}

func (h Handlers) filterProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "filterProfileID")
	if err != nil {
		httputil.BadRequest(w, "invalid filter profile ID")
		return 0, false
	}
	return id, true
}

func (h Handlers) getFilterProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.filterProfileID(w, r)
	if !ok {
		return
	}
	p, err := h.filterProfiles().Get(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "get", "filter profile") {
		return
	}
	httputil.Data(w, filterProfileDTO(p))
}

func (h Handlers) createFilterProfile(w http.ResponseWriter, r *http.Request) {
	var in filterProfileInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	p, err := h.filterProfiles().Create(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), in.service())
	if httputil.ServiceError(w, err, "create", "filter profile") {
		return
	}
	httputil.Created(w, filterProfileDTO(p))
}

func (h Handlers) replaceFilterProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.filterProfileID(w, r)
	if !ok {
		return
	}
	var in filterProfileInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	p, err := h.filterProfiles().Update(r.Context(), auth.TenantIDFromContext(r.Context()), id, in.service())
	if httputil.ServiceError(w, err, "update", "filter profile") {
		return
	}
	httputil.Data(w, filterProfileDTO(p))
}

func (h Handlers) deleteFilterProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.filterProfileID(w, r)
	if !ok {
		return
	}
	err := h.filterProfiles().Delete(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "delete", "filter profile") {
		return
	}
	httputil.NoContent(w)
}

// FilterProfileDefault is the organization's default filter profile for one
// format: the pick a project or template created without a template starts
// with.
type FilterProfileDefault struct {
	Format          string `json:"format"`
	FilterProfileID int64  `json:"filter_profile_id"`
	Name            string `json:"name"`
}

func filterProfileDefaultsDTO(in []filterprofile.Choice) []FilterProfileDefault {
	out := make([]FilterProfileDefault, 0, len(in))
	for _, c := range in {
		out = append(out, FilterProfileDefault{Format: c.Format, FilterProfileID: c.ID, Name: c.Name})
	}
	return out
}

func (h Handlers) setFilterProfileDefault(w http.ResponseWriter, r *http.Request) {
	var in filterprofile.DefaultInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	defaults, err := h.filterProfiles().SetDefault(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), in)
	if httputil.ServiceError(w, err, "set", "filter profile default") {
		return
	}
	httputil.Data(w, filterProfileDefaultsDTO(defaults))
}
