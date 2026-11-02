package publicapi

import (
	"net/http"
	"time"

	"openloc/internal/auth"
	"openloc/internal/httputil"
	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
)

// SegmentationProfile is a v1 segmentation profile: a read-only built-in or
// one of the organization's own.
type SegmentationProfile struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	BuiltinKey *string `json:"builtin_key"`
	// SourceLanguages is empty on a profile that suits any language.
	SourceLanguages []string               `json:"source_languages"`
	Builtin         bool                   `json:"builtin"`
	Description     string                 `json:"description"`
	BasedOnID       *int64                 `json:"based_on_id"`
	Definition      SegmentationDefinition `json:"definition"`
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
}

// SegmentationDefinition is a profile's mode, ordered rules and word lists.
type SegmentationDefinition struct {
	Mode                string             `json:"mode"`
	Rules               []SegmentationRule `json:"rules"`
	Abbreviations       []string           `json:"abbreviations"`
	NumberAbbreviations []string           `json:"number_abbreviations"`
}

// SegmentationRule decides a position: before and after are patterns.
type SegmentationRule struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Break  bool   `json:"break"`
}

// segmentationInput is the body of a create or a replace.
type segmentationInput struct {
	Name            string                  `json:"name"`
	SourceLanguages []string                `json:"source_languages"`
	Definition      *SegmentationDefinition `json:"definition"`
	FromID          *int64                  `json:"from_id"`
	UpdatedAt       *time.Time              `json:"updated_at"`
}

func (in segmentationInput) service() segmentationapi.Input {
	out := segmentationapi.Input{Name: in.Name, SourceLanguages: in.SourceLanguages, From: in.FromID, UpdatedAt: in.UpdatedAt}
	if d := in.Definition; d != nil {
		rs := segmentation.RuleSet{Mode: segmentation.Mode(d.Mode), Abbreviations: d.Abbreviations, NumberAbbreviations: d.NumberAbbreviations}
		for _, r := range d.Rules {
			rs.Rules = append(rs.Rules, segmentation.Rule(r))
		}
		out.Definition = &rs
	}
	return out
}

func segmentationDTO(p segmentationapi.Profile) SegmentationProfile {
	out := SegmentationProfile{
		ID: p.ID, Name: p.Name, SourceLanguages: p.SourceLanguages, Builtin: p.Builtin, Description: p.Description, BasedOnID: p.BasedOn,
		Definition: SegmentationDefinition{
			Mode: string(p.Definition.Mode), Rules: make([]SegmentationRule, len(p.Definition.Rules)),
			Abbreviations: p.Definition.Abbreviations, NumberAbbreviations: p.Definition.NumberAbbreviations,
		},
		// Fractional seconds: a replace sends updated_at back, and it must
		// match the stored value exactly.
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	for i, r := range p.Definition.Rules {
		out.Definition.Rules[i] = SegmentationRule(r)
	}
	if p.BuiltinKey != "" {
		out.BuiltinKey = &p.BuiltinKey
	}
	return out
}

func (h Handlers) segmentation() *segmentationapi.Service {
	return segmentationapi.NewService(h.Queries, h.Pool)
}

func (h Handlers) listSegmentationProfiles(w http.ResponseWriter, r *http.Request) {
	page := httputil.ParseCursor(r, listMaxLimit)
	profiles, err := h.segmentation().ListPage(r.Context(), auth.TenantIDFromContext(r.Context()),
		r.URL.Query().Get("source_language"), page.Cursor, page.Limit+1)
	if httputil.ServiceError(w, err, "list", "segmentation profiles") {
		return
	}
	writePage(w, profiles, page.Limit, func(s segmentationapi.Profile) int64 { return s.ID }, segmentationDTO)
}

func (h Handlers) segmentationProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := httputil.ParseID(r, "profileID")
	if err != nil {
		httputil.BadRequest(w, "invalid segmentation profile ID")
		return 0, false
	}
	return id, true
}

func (h Handlers) getSegmentationProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.segmentationProfileID(w, r)
	if !ok {
		return
	}
	p, err := h.segmentation().Get(r.Context(), auth.TenantIDFromContext(r.Context()), id)
	if httputil.ServiceError(w, err, "get", "segmentation profile") {
		return
	}
	httputil.Data(w, segmentationDTO(p))
}

func (h Handlers) createSegmentationProfile(w http.ResponseWriter, r *http.Request) {
	var in segmentationInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	p, err := h.segmentation().Create(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), in.service())
	if httputil.ServiceError(w, err, "create", "segmentation profile") {
		return
	}
	httputil.Created(w, segmentationDTO(p))
}

func (h Handlers) replaceSegmentationProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := h.segmentationProfileID(w, r)
	if !ok {
		return
	}
	var in segmentationInput
	if err := httputil.Decode(r, &in); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	p, err := h.segmentation().Update(r.Context(), auth.TenantIDFromContext(r.Context()), id, in.service())
	if httputil.ServiceError(w, err, "update", "segmentation profile") {
		return
	}
	httputil.Data(w, segmentationDTO(p))
}
