package terminology

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/tbx"
)

type Handler struct {
	*Service
}

func NewHandler(q db.Querier, pool *pgxpool.Pool) *Handler {
	return &Handler{Service: NewService(q, pool)}
}

// --- Term Bases (Terminology CRUD) ---

func (h *Handler) HandleListBases(w http.ResponseWriter, r *http.Request) {
	bases, err := h.ListBases(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "term bases")
		return
	}
	httputil.Data(w, bases)
}

func (h *Handler) HandleCreateBase(w http.ResponseWriter, r *http.Request) {
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
	base, err := h.CreateBase(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), req.Name, req.Description, req.SourceLanguage)
	if httputil.ServiceError(w, err, "create", "term base") {
		return
	}
	httputil.Created(w, base)
}

func (h *Handler) HandleGetBase(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       id,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "term base") {
		return
	}

	count, _ := h.queries.CountTermBaseEntries(r.Context(), id)

	httputil.Data(w, map[string]any{
		"term_base":   base,
		"entry_count": count,
	})
}

func (h *Handler) HandleUpdateBase(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	// Verify ownership
	if _, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       id,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "term base") {
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := httputil.Decode(r, &req); err != nil || req.Name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}

	base, err := h.queries.UpdateTermBase(r.Context(), db.UpdateTermBaseParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		httputil.ServerError(w, "update", "term base")
		return
	}
	httputil.Data(w, base)
}

func (h *Handler) HandleDeleteBase(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       id,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "term base") {
		return
	}

	if err := h.DeleteBase(r.Context(), id); err != nil {
		httputil.ServerError(w, "delete", "term base")
		return
	}
	httputil.NoContent(w)
}

// --- CSV Import/Export ---

// MaxCSVImportSize caps a CSV term import (10 MB), on every surface.
const MaxCSVImportSize = 10 << 20

// HandleImportCSV handles POST /api/terminology/{baseID}/import.
// Accepts a multipart CSV. A header row is honored (columns mapped by name, any
// order); otherwise the legacy positional order source,target,status,notes,domain.
// Target/source language resolve per row: a source_language/target_language
// column wins, else the ?source=/?target= query params, else the base's pair —
// so a multilingual base imports into any pair and an all-languages export round-
// trips into its original languages.
func (h *Handler) HandleImportCSV(w http.ResponseWriter, r *http.Request) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "term base") {
		return
	}

	userID := auth.UserIDFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, MaxCSVImportSize)
	if err := r.ParseMultipartForm(MaxCSVImportSize); err != nil {
		httputil.BadRequest(w, "file too large or invalid form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

	res, err := h.ImportCSV(r.Context(), base, userID, file,
		r.URL.Query().Get("source"), r.URL.Query().Get("target"))
	if err != nil {
		httputil.ServerError(w, "import", "terms")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"imported": res.Imported,
		"skipped":  res.Skipped,
	})
}

// HandleExportCSV handles GET /api/terminology/{baseID}/export.
// Streams a CSV file of the base's concept/term data, flattened to bilingual rows.
//
// Query params (all optional):
//   - source: source language. Defaults to the base's source_language.
//   - target: target language, repeatable (?target=de&target=fr). Omitted =
//     every other language present. A single target yields the compact 5-column
//     format (source_term,target_term,status,domain,notes) that CSV import round-
//     trips; all/multi adds a target_language column. Notes is always last.
func (h *Handler) HandleExportCSV(w http.ResponseWriter, r *http.Request) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "term base") {
		return
	}

	source := langcode.Normalize(r.URL.Query().Get("source"))
	if source == "" {
		source = base.SourceLanguage
	}
	// target may repeat (?target=de&target=fr). No target = "all languages".
	// A single target keeps the compact 5-column format that CSV import round-
	// trips; all/multi adds a target_language column to disambiguate the rows.
	targets := map[string]bool{}
	for _, t := range r.URL.Query()["target"] {
		if n := langcode.Normalize(t); n != "" && n != source {
			targets[n] = true
		}
	}
	allLangs := len(targets) == 0
	includeLangCol := len(targets) != 1

	rows, err := h.queries.ListConceptTermsForExport(r.Context(), baseID)
	if err != nil {
		httputil.ServerError(w, "export", "term base")
		return
	}

	filename := base.Name + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	httputil.SetAttachmentFilename(w, filename)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	if includeLangCol {
		_ = writer.Write([]string{"source_language", "source_term", "target_language", "target_term", "status", "domain", "notes"})
	} else {
		_ = writer.Write([]string{"source_language", "source_term", "target_term", "status", "domain", "notes"})
	}

	// Rows arrive ordered by concept_id, so we can group by scanning a contiguous
	// run. For each concept, emit the cross-product of its source-language terms
	// (synonyms) × its target terms; status/notes belong to the target term,
	// domain to the concept.
	writeConcept := func(group []db.ListConceptTermsForExportRow) {
		if len(group) == 0 {
			return
		}
		domain := group[0].Domain
		var sources []string
		for _, t := range group {
			if langcode.Normalize(t.Language) == source {
				sources = append(sources, t.Text)
			}
		}
		if len(sources) == 0 {
			sources = []string{""} // still export target terms when no source-lang term exists
		}
		for _, t := range group {
			lang := langcode.Normalize(t.Language)
			if lang == source {
				continue // never pair a term with itself
			}
			if !allLangs && !targets[lang] {
				continue
			}
			for _, src := range sources {
				if includeLangCol {
					_ = writer.Write([]string{source, src, t.Language, t.Text, t.Status, domain, t.Notes})
				} else {
					_ = writer.Write([]string{source, src, t.Text, t.Status, domain, t.Notes})
				}
			}
		}
	}

	var group []db.ListConceptTermsForExportRow
	var curConcept int64
	for _, row := range rows {
		if len(group) > 0 && row.ConceptID != curConcept {
			writeConcept(group)
			group = group[:0]
		}
		curConcept = row.ConceptID
		group = append(group, row)
	}
	writeConcept(group)
}

// HandleListBaseLanguages handles GET /api/terminology/{baseID}/languages.
// Returns the distinct languages present in the base, so the CSV export UI can
// offer a real target-language picker for a multilingual base.
func (h *Handler) HandleListBaseLanguages(w http.ResponseWriter, r *http.Request) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	if _, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: tenantID,
	}); httputil.DBError(w, err, "term base") {
		return
	}

	langs, err := h.queries.ListTermBaseLanguages(r.Context(), baseID)
	if err != nil {
		httputil.ServerError(w, "list", "languages")
		return
	}
	if langs == nil {
		langs = []string{}
	}
	httputil.Data(w, langs)
}

const (
	// The parser streams; this caps the upload, and anything over the memory
	// threshold spills to disk.
	maxTBXUploadSize      = 256 << 20 // 256 MB total request cap
	tbxUploadMemThreshold = 32 << 20  // buffer up to 32 MB in memory, rest to disk
)

// HandleImportTBX handles POST /api/terminology/{baseID}/import-tbx.
// Parses a TBX file (v2 martif or v3 tbx, DCA style, any dialect via the mapping
// registry) into full multilingual concepts — all languages, synonyms, and
// per-term status preserved.
func (h *Handler) HandleImportTBX(w http.ResponseWriter, r *http.Request) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "term base") {
		return
	}

	userID := auth.UserIDFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxTBXUploadSize)
	if err := r.ParseMultipartForm(tbxUploadMemThreshold); err != nil {
		httputil.BadRequest(w, "file too large or invalid form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "missing file field")
		return
	}
	defer file.Close()

	res, err := h.ImportTBX(r.Context(), base, userID, file)
	var ierr ImportError
	if errors.As(err, &ierr) {
		httputil.BadRequest(w, ierr.Msg)
		return
	}
	if err != nil {
		httputil.ServerError(w, "import", "terms")
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{
		"imported": res.Imported,
		"terms":    res.Terms,
		"skipped":  res.Skipped,
	})
}

// HandleExportTBX handles GET /api/terminology/{baseID}/export-tbx.
// Streams a TBX-Basic v3 file with all entries.
func (h *Handler) HandleExportTBX(w http.ResponseWriter, r *http.Request) {
	baseID, err := httputil.ParseID(r, "baseID")
	if err != nil {
		httputil.BadRequest(w, "invalid term base ID")
		return
	}

	tenantID := auth.TenantIDFromContext(r.Context())
	base, err := h.queries.GetTermBase(r.Context(), db.GetTermBaseParams{
		ID:       baseID,
		TenantID: tenantID,
	})
	if httputil.DBError(w, err, "term base") {
		return
	}

	// Load concept+term rows (ordered by concept) and group into full
	// multilingual concepts before writing, so a load error is a clean 500 rather
	// than a truncated download.
	rows, err := h.queries.ListConceptTermsForExport(r.Context(), baseID)
	if err != nil {
		httputil.ServerError(w, "export", "terms")
		return
	}
	var concepts []tbx.Concept
	var cur tbx.Concept
	haveCur := false
	var curID int64
	for _, row := range rows {
		if !haveCur || row.ConceptID != curID {
			if haveCur {
				concepts = append(concepts, cur)
			}
			cur = tbx.Concept{Definition: row.Definition, Domain: row.Domain}
			curID = row.ConceptID
			haveCur = true
		}
		cur.Terms = append(cur.Terms, tbx.ConceptTerm{
			Language:     row.Language,
			Text:         row.Text,
			Status:       row.Status,
			PartOfSpeech: row.PartOfSpeech,
			Notes:        row.Notes,
		})
	}
	if haveCur {
		concepts = append(concepts, cur)
	}

	filename := base.Name + ".tbx"
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	httputil.SetAttachmentFilename(w, filename)

	// The root xml:lang is the base's default/source language (administrative).
	if err := tbx.WriteHeader(w, base.SourceLanguage); err != nil {
		return
	}
	if err := tbx.WriteConceptEntries(w, concepts, 0); err != nil {
		return
	}
	_ = tbx.WriteFooter(w)
}

// --- CAT Editor Term Lookup ---

// HandleLookup handles GET /api/terminology/lookup?project_id=X&source_language=Y&target_language=Z&source_text=...
// Returns matching terms for a given source text from all assigned term bases.
// Also accepts template_id for backwards compatibility (template assignments).
// Both project_id and template_id resolve to a settings_profile_id and use the same query.
func (h *Handler) HandleLookup(w http.ResponseWriter, r *http.Request) {
	projectIDStr := r.URL.Query().Get("project_id")
	templateIDStr := r.URL.Query().Get("template_id")
	srcLang := r.URL.Query().Get("source_language")
	tgtLang := r.URL.Query().Get("target_language")
	sourceText := r.URL.Query().Get("source_text")

	if projectIDStr == "" && templateIDStr == "" || srcLang == "" || tgtLang == "" || sourceText == "" {
		httputil.BadRequest(w, "project_id (or template_id), source_language, target_language, and source_text are required")
		return
	}

	// Resolve settings profile ID from either project or template. A project also
	// carries a domain, used to soft-prefer domain-matching concepts (templates
	// have none → no preference).
	var settingsProfileID int64
	var projectDomain string
	if projectIDStr != "" {
		projectID, parseErr := strconv.ParseInt(projectIDStr, 10, 64)
		if parseErr != nil {
			httputil.BadRequest(w, "invalid project_id")
			return
		}
		tenantID := auth.TenantIDFromContext(r.Context())
		project, err := h.queries.GetProject(r.Context(), db.GetProjectParams{
			ID: projectID, TenantID: tenantID,
		})
		if err != nil {
			httputil.NotFound(w, "project")
			return
		}
		settingsProfileID = project.SettingsProfileID
		// Domain is a built-in label, so the value comes from the project's
		// pick. No pick = no domain preference, which is the same as the empty
		// string the ranking already treats as "do not prefer".
		if v, err := h.queries.GetProjectSystemLabelValue(r.Context(), db.GetProjectSystemLabelValueParams{
			ProjectID: projectID, TenantID: tenantID, SystemKey: &domainSystemKey,
		}); err == nil {
			projectDomain = v.Value
		}
	} else {
		templateID, parseErr := strconv.ParseInt(templateIDStr, 10, 64)
		if parseErr != nil {
			httputil.BadRequest(w, "invalid template_id")
			return
		}
		tenantID := auth.TenantIDFromContext(r.Context())
		tmpl, err := h.queries.GetTemplate(r.Context(), db.GetTemplateParams{
			ID: templateID, TenantID: tenantID,
		})
		if err != nil {
			httputil.NotFound(w, "template")
			return
		}
		settingsProfileID = tmpl.SettingsProfileID
	}

	tokens := tokenizeSourceText(sourceText, 4)
	terms, err := h.queries.LookupSettingsTermsByTokens(r.Context(), db.LookupSettingsTermsByTokensParams{
		SettingsProfileID: settingsProfileID,
		SourceLanguage:    srcLang,
		TargetLanguage:    tgtLang,
		Tokens:            tokens,
		ProjectDomain:     projectDomain,
	})
	if err != nil {
		httputil.ServerError(w, "lookup", "terms")
		return
	}
	httputil.Data(w, terms)
}

// --- Helpers ---

// domainSystemKey addresses the built-in Domain label. A package-level var so
// the query can take its address; the key itself is fixed by the migration.
var domainSystemKey = "domain"

func isValidStatus(s string) bool {
	return s == "approved" || s == "forbidden" || s == "pending"
}
