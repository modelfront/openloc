package publicapi

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/terminology"
)

// maxGlossaryImportSize caps one TBX or CSV upload.
const maxGlossaryImportSize = 256 << 20

// Glossary is a v1 glossary (term base).
type Glossary struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	ConceptCount    int64    `json:"concept_count"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func glossaryDTO(b db.TermBasis, concepts int64) Glossary {
	targets := b.TargetLanguages
	if targets == nil {
		targets = []string{}
	}
	return Glossary{ID: b.ID, Name: b.Name, Description: b.Description, SourceLanguage: b.SourceLanguage,
		TargetLanguages: targets, ConceptCount: concepts, CreatedAt: timestamp(b.CreatedAt), UpdatedAt: timestamp(b.UpdatedAt)}
}

// Concept is one meaning with its terms in any number of languages.
type Concept struct {
	ID         int64  `json:"id"`
	Definition string `json:"definition"`
	Domain     string `json:"domain"`
	Terms      []Term `json:"terms"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Term is one term of a concept, in one language.
type Term struct {
	ID           int64  `json:"id"`
	Language     string `json:"language"`
	Text         string `json:"text"`
	Status       string `json:"status"`
	PartOfSpeech string `json:"part_of_speech"`
	Notes        string `json:"notes"`
}

func conceptDTO(c terminology.ConceptDTO) Concept {
	terms := make([]Term, len(c.Terms))
	for i, t := range c.Terms {
		terms[i] = Term{ID: t.ID, Language: t.Language, Text: t.Text, Status: t.Status, PartOfSpeech: t.PartOfSpeech, Notes: t.Notes}
	}
	return Concept{ID: c.ID, Definition: c.Definition, Domain: c.Domain, Terms: terms,
		CreatedAt: requiredRFC3339(c.CreatedAt), UpdatedAt: requiredRFC3339(c.UpdatedAt)}
}

// glossaryFromRoute loads the {glossaryID} term base of the caller's tenant.
func (h Handlers) glossaryFromRoute(w http.ResponseWriter, r *http.Request) (db.TermBasis, bool) {
	id, err := httputil.ParseID(r, "glossaryID")
	if err != nil {
		httputil.BadRequest(w, "invalid glossary ID")
		return db.TermBasis{}, false
	}
	base, err := h.Queries.GetTermBase(r.Context(), db.GetTermBaseParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "glossary") {
		return db.TermBasis{}, false
	}
	return base, true
}

func (h Handlers) writeGlossary(w http.ResponseWriter, r *http.Request, base db.TermBasis, status int) {
	n, err := h.Queries.CountTermBaseEntries(r.Context(), base.ID)
	if err != nil {
		httputil.ServerError(w, "count", "concepts")
		return
	}
	httputil.JSON(w, status, map[string]any{"data": glossaryDTO(base, n)})
}

func (h Handlers) listGlossaries(w http.ResponseWriter, r *http.Request) {
	bases, err := h.Glossaries.ListBases(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "glossaries")
		return
	}
	writeAll(w, bases, func(b terminology.BaseWithStats) Glossary {
		return glossaryDTO(db.TermBasis{ID: b.ID, Name: b.Name, Description: b.Description, SourceLanguage: b.SourceLanguage,
			TargetLanguages: b.TargetLanguages, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}, b.EntryCount)
	})
}

func (h Handlers) createGlossary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		SourceLanguage string `json:"source_language"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	base, err := h.Glossaries.CreateBase(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), req.Name, req.Description, req.SourceLanguage)
	if httputil.ServiceError(w, err, "create", "glossary") {
		return
	}
	httputil.Created(w, glossaryDTO(base, 0))
}

func (h Handlers) getGlossary(w http.ResponseWriter, r *http.Request) {
	if base, ok := h.glossaryFromRoute(w, r); ok {
		h.writeGlossary(w, r, base, http.StatusOK)
	}
}

func (h Handlers) updateGlossary(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name, desc := base.Name, base.Description
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		desc = *req.Description
	}
	if name == "" {
		httputil.FieldError(w, http.StatusBadRequest, "name", "name must not be empty")
		return
	}
	updated, err := h.Queries.UpdateTermBase(r.Context(), db.UpdateTermBaseParams{
		ID: base.ID, Name: name, Description: desc, // tenant checked by glossaryFromRoute
	})
	if httputil.DBError(w, err, "glossary") {
		return
	}
	h.writeGlossary(w, r, updated, http.StatusOK)
}

func (h Handlers) deleteGlossary(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	if err := h.Glossaries.DeleteBase(r.Context(), base.ID); err != nil {
		httputil.ServerError(w, "delete", "glossary")
		return
	}
	httputil.NoContent(w)
}

func (h Handlers) listConcepts(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page := httputil.ParseCursor(r, listMaxLimit)
	res, err := h.Glossaries.ListConcepts(r.Context(), base.ID, terminology.ConceptFilter{
		Search:    strings.TrimSpace(q.Get("q")),
		Langs:     q["language"],
		Cursor:    page.Cursor,
		Limit:     page.Limit + 1, // +1 to detect has_more
		SkipTotal: true,
	})
	if err != nil {
		httputil.ServerError(w, "list", "concepts")
		return
	}
	writePage(w, res.Concepts, page.Limit, func(c terminology.ConceptDTO) int64 { return c.ID }, conceptDTO)
}

func (h Handlers) createConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	var req struct {
		Definition string `json:"definition"`
		Domain     string `json:"domain"`
		Terms      []Term `json:"terms"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	in := terminology.ConceptInput{Definition: strings.TrimSpace(req.Definition), Domain: strings.TrimSpace(req.Domain)}
	for _, t := range req.Terms {
		if strings.TrimSpace(t.Language) == "" || strings.TrimSpace(t.Text) == "" {
			httputil.FieldError(w, http.StatusBadRequest, "terms", "every term needs a language and text")
			return
		}
		in.Terms = append(in.Terms, terminology.ConceptTermInput{Language: t.Language, Text: strings.TrimSpace(t.Text),
			Status: t.Status, PartOfSpeech: t.PartOfSpeech, Notes: t.Notes})
	}
	if len(in.Terms) == 0 {
		httputil.FieldError(w, http.StatusBadRequest, "terms", "a concept needs at least one term")
		return
	}
	ctx := r.Context()
	c, err := h.Glossaries.AddConcept(ctx, base, in, auth.UserIDFromContext(ctx))
	if httputil.ServiceError(w, err, "create", "concept") {
		return
	}
	httputil.Created(w, conceptDTO(c))
}

func (h Handlers) deleteConcept(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	conceptID, err := httputil.ParseID(r, "conceptID")
	if err != nil {
		httputil.BadRequest(w, "invalid concept ID")
		return
	}
	err = h.Glossaries.DeleteConcept(r.Context(), base.ID, conceptID, auth.UserIDFromContext(r.Context()))
	if httputil.DBError(w, err, "concept") {
		return
	}
	httputil.NoContent(w)
}

// importGlossary adds the concepts of a TBX or CSV upload. The format comes
// from the `format` field, else from the file extension.
func (h Handlers) importGlossary(w http.ResponseWriter, r *http.Request) {
	base, ok := h.glossaryFromRoute(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxGlossaryImportSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil { // the rest spills to disk
		httputil.BadRequest(w, "file too large or invalid multipart form")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		httputil.FieldError(w, http.StatusBadRequest, "file", "missing file field")
		return
	}
	defer f.Close()
	format := strings.ToLower(strings.TrimSpace(r.FormValue("format")))
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(path.Ext(header.Filename)), ".")
	}
	ctx := r.Context()
	userID := auth.UserIDFromContext(ctx)
	var res terminology.ImportResult
	switch format {
	case "tbx":
		res, err = h.Glossaries.ImportTBX(ctx, base, userID, f)
	case "csv":
		if header.Size > terminology.MaxCSVImportSize {
			httputil.FieldError(w, http.StatusBadRequest, "file", "a CSV import may be at most 10 MB")
			return
		}
		res, err = h.Glossaries.ImportCSV(ctx, base, userID, f, r.FormValue("source_language"), r.FormValue("target_language"))
	default:
		httputil.FieldError(w, http.StatusBadRequest, "format", "format must be tbx or csv")
		return
	}
	var ierr terminology.ImportError
	if errors.As(err, &ierr) {
		httputil.BadRequest(w, ierr.Msg)
		return
	}
	if err != nil {
		httputil.ServerError(w, "import", "glossary")
		return
	}
	httputil.Data(w, map[string]int{"imported": res.Imported, "terms": res.Terms, "skipped": res.Skipped})
}

// exportGlossary streams the glossary as TBX (the default) or CSV through the
// app's export handlers, which read the base id from {baseID}.
func (h Handlers) exportGlossary(w http.ResponseWriter, r *http.Request) {
	chi.RouteContext(r.Context()).URLParams.Add("baseID", chi.URLParam(r, "glossaryID"))
	switch r.URL.Query().Get("format") {
	case "", "tbx":
		h.Glossaries.HandleExportTBX(w, r)
	case "csv":
		h.Glossaries.HandleExportCSV(w, r)
	default:
		httputil.FieldError(w, http.StatusBadRequest, "format", "format must be tbx or csv")
	}
}
