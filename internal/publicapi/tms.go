package publicapi

import (
	"errors"
	"net/http"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/translationmemory"
)

// TM is a v1 translation memory.
type TM struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	SourceLanguage  *string  `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	EntryCount      int64    `json:"entry_count"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func tmDTO(c db.TmCollection, entries int64) TM {
	targets := c.TargetLanguages
	if targets == nil {
		targets = []string{}
	}
	return TM{ID: c.ID, Name: c.Name, SourceLanguage: c.SourceLanguage, TargetLanguages: targets,
		EntryCount: entries, CreatedAt: timestamp(c.CreatedAt), UpdatedAt: timestamp(c.UpdatedAt)}
}

// TMEntry is one translation unit. score is set on a search: how close the
// source is to the query, from 0 to 1.
type TMEntry struct {
	ID             int64    `json:"id"`
	SourceLanguage string   `json:"source_language"`
	TargetLanguage string   `json:"target_language"`
	SourceText     string   `json:"source_text"`
	TargetText     string   `json:"target_text"`
	Score          *float64 `json:"score,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

// TMImport is the progress of one TMX import.
type TMImport struct {
	ID           int64   `json:"id"`
	FileName     string  `json:"file_name"`
	Status       string  `json:"status"`
	TotalUnits   int32   `json:"total_units"`
	Imported     int32   `json:"imported"`
	Skipped      int32   `json:"skipped"`
	ErrorMessage *string `json:"error_message"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func tmImportDTO(i db.TmImport) TMImport {
	return TMImport{ID: i.ID, FileName: i.FileName, Status: i.Status, TotalUnits: i.TotalUnits, Imported: i.Imported,
		Skipped: i.Skipped, ErrorMessage: i.ErrorMessage, CreatedAt: timestamp(i.CreatedAt), UpdatedAt: timestamp(i.UpdatedAt)}
}

// writeTMError maps a translation memory service error.
func writeTMError(w http.ResponseWriter, err error, verb string) {
	var verr translationmemory.ValidationError
	if errors.As(err, &verr) {
		httputil.BadRequest(w, verr.Msg)
		return
	}
	var inUse translationmemory.InUseError
	if errors.As(err, &inUse) {
		httputil.ErrorCode(w, http.StatusConflict, "in_use", inUse.Error())
		return
	}
	httputil.ServiceError(w, err, verb, "TM")
}

func (h Handlers) listTMs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.ListTMCollections(r.Context(), auth.TenantIDFromContext(r.Context()))
	if err != nil {
		httputil.ServerError(w, "list", "TMs")
		return
	}
	writeAll(w, rows, func(c db.ListTMCollectionsRow) TM {
		return tmDTO(db.TmCollection{ID: c.ID, Name: c.Name, SourceLanguage: c.SourceLanguage, TargetLanguages: c.TargetLanguages,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}, c.EntryCount)
	})
}

func (h Handlers) createTM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string   `json:"name"`
		SourceLanguage  string   `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
	}
	if err := httputil.Decode(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	ctx := r.Context()
	tm, err := h.TMs.CreateCollection(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), translationmemory.CreateCollectionInput{
		Name: req.Name, SourceLanguage: req.SourceLanguage, TargetLanguages: req.TargetLanguages,
	})
	if err != nil {
		writeTMError(w, err, "create")
		return
	}
	httputil.Created(w, tmDTO(tm, 0))
}

// tmFromRoute loads the {tmID} TM of the caller's tenant.
func (h Handlers) tmFromRoute(w http.ResponseWriter, r *http.Request) (db.TmCollection, bool) {
	id, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return db.TmCollection{}, false
	}
	tm, err := h.Queries.GetTMCollection(r.Context(), db.GetTMCollectionParams{ID: id, TenantID: auth.TenantIDFromContext(r.Context())})
	if httputil.DBError(w, err, "TM") {
		return db.TmCollection{}, false
	}
	return tm, true
}

func (h Handlers) getTM(w http.ResponseWriter, r *http.Request) {
	tm, ok := h.tmFromRoute(w, r)
	if !ok {
		return
	}
	n, err := h.Queries.CountTMEntries(r.Context(), db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil {
		httputil.ServerError(w, "count", "TM entries")
		return
	}
	httputil.Data(w, tmDTO(tm, n))
}

func (h Handlers) renameTM(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r, "tmID")
	if err != nil {
		httputil.BadRequest(w, "invalid TM ID")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httputil.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httputil.FieldError(w, http.StatusBadRequest, "name", "name is required")
		return
	}
	_, err = h.Queries.RenameTMCollection(r.Context(), db.RenameTMCollectionParams{
		ID: id, Name: strings.TrimSpace(req.Name), TenantID: auth.TenantIDFromContext(r.Context()),
	})
	if httputil.DBError(w, err, "TM") {
		return
	}
	h.getTM(w, r)
}

func (h Handlers) listTMEntries(w http.ResponseWriter, r *http.Request) {
	tm, ok := h.tmFromRoute(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page := httputil.ParseCursor(r, listMaxLimit)
	res, err := h.TMs.ListEntries(r.Context(), tm.ID, translationmemory.EntryFilter{
		SourceLanguage: langcode.Normalize(q.Get("source_language")),
		TargetLanguage: langcode.Normalize(q.Get("target_language")),
		Query:          strings.TrimSpace(q.Get("q")),
		Cursor:         page.Cursor,
		Limit:          page.Limit + 1, // +1 to detect has_more
		SkipTotal:      true,
	})
	if err != nil {
		writeTMError(w, err, "list")
		return
	}
	if res.Matches != nil {
		writePage(w, res.Matches, page.Limit, func(e db.SearchTMEntriesCursorRow) int64 { return e.ID }, func(e db.SearchTMEntriesCursorRow) TMEntry {
			out := TMEntry{ID: e.ID, SourceLanguage: e.SourceLanguage, TargetLanguage: e.TargetLanguage, SourceText: e.SourceText,
				TargetText: e.TargetText, CreatedAt: timestamp(e.CreatedAt), UpdatedAt: timestamp(e.UpdatedAt)}
			switch s := e.Score.(type) {
			case float64:
				out.Score = &s
			case float32:
				f := float64(s)
				out.Score = &f
			}
			return out
		})
		return
	}
	writePage(w, res.Entries, page.Limit, func(e db.TranslationMemory) int64 { return e.ID }, func(e db.TranslationMemory) TMEntry {
		return TMEntry{ID: e.ID, SourceLanguage: e.SourceLanguage, TargetLanguage: e.TargetLanguage, SourceText: e.SourceText,
			TargetText: e.TargetText, CreatedAt: timestamp(e.CreatedAt), UpdatedAt: timestamp(e.UpdatedAt)}
	})
}

func (h Handlers) importTMX(w http.ResponseWriter, r *http.Request) {
	imp, ok := h.TMs.Import(w, r)
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusAccepted, map[string]any{"data": tmImportDTO(imp)})
}

func (h Handlers) listTMImports(w http.ResponseWriter, r *http.Request) {
	tm, ok := h.tmFromRoute(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListTMImports(r.Context(), tm.ID)
	if err != nil {
		httputil.ServerError(w, "list", "imports")
		return
	}
	writeAll(w, rows, tmImportDTO)
}
