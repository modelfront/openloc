package terminology

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// tsString renders a nullable timestamp as RFC3339, or "" when unset.
func tsString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format(time.RFC3339)
}

// termRowDTO maps a listed term row (with creator/updater join) to its DTO.
func termRowDTO(t db.ListTermsForConceptsRow) ConceptTermDTO {
	return ConceptTermDTO{
		ID: t.ID, Language: t.Language, Text: t.Text,
		Status: t.Status, PartOfSpeech: t.PartOfSpeech, Notes: t.Notes,
		CreatedAt: tsString(t.CreatedAt), UpdatedAt: tsString(t.UpdatedAt),
		CreatedBy: t.CreatedByName, UpdatedBy: t.UpdatedByName,
	}
}

// --- Concept-centric CRUD ---
//
// The browser is concept-centric: a concept groups TERMS across any number of
// languages, each term with its own text/status/part-of-speech. These handlers
// expose the concept + term CRUD the editor needs. Tenant isolation: every
// request first verifies base ownership (GetTermBase by tenant); concept/term
// ops additionally verify the concept belongs to that base (GetConcept), so an
// id from another tenant resolves to 404.

type ConceptTermDTO struct {
	ID           int64  `json:"id"`
	Language     string `json:"language"`
	Text         string `json:"text"`
	Status       string `json:"status"`
	PartOfSpeech string `json:"part_of_speech"`
	Notes        string `json:"notes"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	CreatedBy    string `json:"created_by"` // user name, "" if unknown
	UpdatedBy    string `json:"updated_by"`
}

type ConceptDTO struct {
	ID         int64            `json:"id"`
	Definition string           `json:"definition"`
	Domain     string           `json:"domain"`
	CreatedAt  string           `json:"created_at"`
	UpdatedAt  string           `json:"updated_at"`
	CreatedBy  string           `json:"created_by"` // user name, "" if unknown
	UpdatedBy  string           `json:"updated_by"`
	Terms      []ConceptTermDTO `json:"terms"`
}

type conceptTermInput struct {
	Language     string `json:"language"`
	Text         string `json:"text"`
	Status       string `json:"status"`
	PartOfSpeech string `json:"part_of_speech"`
	Notes        string `json:"notes"`
}

// baseForRequest parses baseID and verifies tenant ownership. On failure it
// writes the response and returns ok=false.
func (h *Handler) baseForRequest(w http.ResponseWriter, r *http.Request) (db.TermBasis, bool) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return db.TermBasis{}, false
	}
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: auth.TenantIDFromContext(r.Context()),
	})
	if httputil.DBError(w, err, "term base") {
		return db.TermBasis{}, false
	}
	return base, true
}

// HandleListConcepts handles GET /api/terminology/{baseID}/concepts.
// Cursor-paginated (keyset by concept id); ?q= filters by domain, definition,
// or any term text.
func (h *Handler) HandleListConcepts(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	f := ConceptFilter{Search: r.URL.Query().Get("q"), Langs: r.URL.Query()["lang"]}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 32); err == nil && n > 0 && n <= 500 {
			f.Limit = int32(n)
		}
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n >= 0 {
			f.Cursor = n
		}
	}
	page, err := h.ListConcepts(r.Context(), base.ID, f)
	if err != nil {
		httputil.ServerError(w, "list", "concepts")
		return
	}
	httputil.Paginated(w, page.Concepts, page.NextCursor, page.HasMore, page.Total)
}

// HandleGetConcept handles GET /api/terminology/{baseID}/concepts/{conceptID}.
func (h *Handler) HandleGetConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return
	}
	dto, err := h.GetConcept(r.Context(), base.ID, conceptID)
	if httputil.DBError(w, err, "concept") {
		return
	}
	httputil.Data(w, dto)
}

// HandleCreateConcept handles POST /api/terminology/{baseID}/concepts.
func (h *Handler) HandleCreateConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	var req struct {
		Definition string             `json:"definition"`
		Domain     string             `json:"domain"`
		Terms      []conceptTermInput `json:"terms"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	terms := validTerms(req.Terms)
	if len(terms) == 0 {
		httputil.BadRequest(w, "a concept needs at least one term with text and a language")
		return
	}

	in := ConceptInput{Definition: req.Definition, Domain: req.Domain}
	for _, t := range terms {
		in.Terms = append(in.Terms, ConceptTermInput{Language: t.Language, Text: t.Text, Status: t.Status, PartOfSpeech: t.PartOfSpeech, Notes: t.Notes})
	}
	c, err := h.AddConcept(r.Context(), base, in, auth.UserIDFromContext(r.Context()))
	if httputil.ServiceError(w, err, "create", "concept") {
		return
	}
	httputil.Created(w, c)
}

// HandleUpdateConcept handles PATCH /api/terminology/{baseID}/concepts/{conceptID}
// (definition + domain only; term edits go through the term endpoints).
func (h *Handler) HandleUpdateConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return
	}
	if _, err := h.queries.GetConcept(r.Context(), db.GetConceptParams{ID: conceptID, TermBaseID: base.ID}); httputil.DBError(w, err, "concept") {
		return
	}
	var req struct {
		Definition string `json:"definition"`
		Domain     string `json:"domain"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	if err := h.queries.UpdateTermConcept(r.Context(), db.UpdateTermConceptParams{
		ID: conceptID, Definition: req.Definition, Domain: req.Domain, UpdatedBy: &userID,
	}); err != nil {
		httputil.ServerError(w, "update", "concept")
		return
	}
	httputil.NoContent(w)
}

// HandleDeleteConcept handles DELETE /api/terminology/{baseID}/concepts/{conceptID}
// (soft delete — hidden everywhere, kept for audit/restore).
func (h *Handler) HandleDeleteConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return
	}
	err = h.DeleteConcept(r.Context(), base.ID, conceptID, auth.UserIDFromContext(r.Context()))
	if httputil.DBError(w, err, "concept") {
		return
	}
	httputil.NoContent(w)
}

// HandleAddTerm handles POST /api/terminology/{baseID}/concepts/{conceptID}/terms.
func (h *Handler) HandleAddTerm(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return
	}
	if _, err := h.queries.GetConcept(r.Context(), db.GetConceptParams{ID: conceptID, TermBaseID: base.ID}); httputil.DBError(w, err, "concept") {
		return
	}
	var req conceptTermInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	t, ok := normalizeTerm(req)
	if !ok {
		httputil.BadRequest(w, "term needs a language and non-empty text")
		return
	}
	// Dedup: a concept never holds two identical terms in a language. If it's
	// already there, return the existing one (idempotent add) instead of a dup.
	if existing, err := h.queries.FindConceptTerm(r.Context(), db.FindConceptTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text,
	}); err == nil {
		httputil.Created(w, ConceptTermDTO{
			ID: existing.ID, Language: existing.Language, Text: existing.Text,
			Status: existing.Status, PartOfSpeech: existing.PartOfSpeech, Notes: existing.Notes,
		})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		httputil.ServerError(w, "check", "term")
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	created, err := h.queries.CreateTerm(r.Context(), db.CreateTermParams{
		ConceptID: conceptID, Language: t.Language, Text: t.Text, Status: t.Status,
		PartOfSpeech: t.PartOfSpeech, Notes: t.Notes, CreatedBy: &userID, UpdatedBy: &userID,
	})
	if err != nil {
		httputil.ServerError(w, "create", "term")
		return
	}
	h.syncBaseLanguages(r.Context(), base.ID)
	httputil.Created(w, ConceptTermDTO{
		ID: created.ID, Language: created.Language, Text: created.Text,
		Status: created.Status, PartOfSpeech: created.PartOfSpeech, Notes: created.Notes,
	})
}

// HandleUpdateTerm handles PATCH /{baseID}/concepts/{conceptID}/terms/{termID}.
func (h *Handler) HandleUpdateTerm(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	conceptID, termID, ok := h.termInConcept(w, r, base)
	if !ok {
		return
	}
	var req conceptTermInput
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	t, valid := normalizeTerm(req)
	if !valid {
		httputil.BadRequest(w, "term needs a language and non-empty text")
		return
	}
	userID := auth.UserIDFromContext(r.Context())
	if err := h.queries.UpdateTermFull(r.Context(), db.UpdateTermFullParams{
		ID: termID, Text: t.Text, Status: t.Status, PartOfSpeech: t.PartOfSpeech,
		Notes: t.Notes, UpdatedBy: &userID,
	}); err != nil {
		httputil.ServerError(w, "update", "term")
		return
	}
	_ = conceptID
	httputil.NoContent(w)
}

// HandleDeleteTerm handles DELETE /{baseID}/concepts/{conceptID}/terms/{termID}.
func (h *Handler) HandleDeleteTerm(w http.ResponseWriter, r *http.Request) {
	base, ok := h.baseForRequest(w, r)
	if !ok {
		return
	}
	_, termID, ok := h.termInConcept(w, r, base)
	if !ok {
		return
	}
	if err := h.DeleteTerm(r.Context(), base.ID, termID, auth.UserIDFromContext(r.Context())); err != nil {
		httputil.ServerError(w, "delete", "term")
		return
	}
	httputil.NoContent(w)
}

// termInConcept parses conceptID + termID and verifies the concept is in the base
// and the term is in the concept (both tenant-scoped). Writes the response and
// returns ok=false on any mismatch (404).
func (h *Handler) termInConcept(w http.ResponseWriter, r *http.Request, base db.TermBasis) (conceptID, termID int64, ok bool) {
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return 0, 0, false
	}
	termID, err = httputil.ParseID(r, "termID")
	if err != nil {
		httputil.BadRequest(w, "invalid term ID")
		return 0, 0, false
	}
	if _, err := h.TermInConcept(r.Context(), base.ID, conceptID, termID); httputil.DBError(w, err, "term") {
		return 0, 0, false
	}
	return conceptID, termID, true
}

// normalizeTerm validates + normalizes a single term input. ok=false when it has
// no language or empty text.
func normalizeTerm(in conceptTermInput) (conceptTermInput, bool) {
	lang := langcode.Normalize(in.Language)
	text := in.Text
	if lang == "" || len(text) == 0 {
		return conceptTermInput{}, false
	}
	status := in.Status
	if !isValidStatus(status) {
		status = "approved"
	}
	return conceptTermInput{
		Language: lang, Text: text, Status: status,
		PartOfSpeech: in.PartOfSpeech, Notes: in.Notes,
	}, true
}

// validTerms normalizes + drops invalid terms, and de-dupes: a concept never
// holds two identical terms in a language (first occurrence wins).
func validTerms(in []conceptTermInput) []conceptTermInput {
	out := make([]conceptTermInput, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, t := range in {
		nt, ok := normalizeTerm(t)
		if !ok {
			continue
		}
		key := nt.Language + "\x00" + nt.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, nt)
	}
	return out
}
