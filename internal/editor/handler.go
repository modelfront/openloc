// Package editor serves the CAT editor's view of a job: a light index of every
// segment, the text of the rows on screen, and the lookups that need the whole
// file's text (search, cross-references, a word's occurrences), so the browser
// never has to load all of it.
package editor

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/placeable"
)

// MaxRows caps the rows one request may ask for.
const MaxRows = 500

// maxWordLength caps the word a candidate lookup searches for.
const maxWordLength = 200

// Handler serves the /api/jobs/{jobID}/editor/* routes. The route group's
// job ownership and linguist assignment gates apply; each handler resolves
// the job tenant-scoped again.
type Handler struct {
	queries db.Querier
}

func NewHandler(q db.Querier) *Handler {
	return &Handler{queries: q}
}

func (h *Handler) job(w http.ResponseWriter, r *http.Request) (db.Job, int64, bool) {
	jobID, err := httputil.ParseID(r, "jobID")
	if err != nil {
		httputil.BadRequest(w, "invalid job ID")
		return db.Job{}, 0, false
	}
	tenantID := auth.TenantIDFromContext(r.Context())
	job, err := h.queries.GetJobTenant(r.Context(), db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if httputil.DBError(w, err, "job") {
		return db.Job{}, 0, false
	}
	return job, tenantID, true
}

// HandleIndex answers GET /api/jobs/{jobID}/editor/index.
func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	job, tenantID, ok := h.job(w, r)
	if !ok {
		return
	}
	rows, err := h.queries.ListEditorIndex(r.Context(), db.ListEditorIndexParams{
		TargetLanguage: job.TargetLanguage, FileID: job.FileID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "editor index")
		return
	}
	httputil.Data(w, buildIndex(rows))
}

// Row is one segment with its translation in the job's language, if any.
// PreviousStepText is the text the translation held when its last finished
// workflow step round ended: the baseline the editor's track changes mark
// against. Nil when no round has finished or it ended with no text.
// Placeables are the source's placeables, in text order, for the editor to
// mark and offer for insertion; in the project's source language.
type Row struct {
	Segment          db.Segment            `json:"segment"`
	Translation      *db.Translation       `json:"translation"`
	PreviousStepText *string               `json:"previous_step_text"`
	Placeables       []placeable.Placeable `json:"placeables"`
}

// HandleRows answers GET /api/jobs/{jobID}/editor/rows?segment_ids=1,2,3: the
// segments of the job's file among those ids, in document order. Ids outside
// the file are not returned.
func (h *Handler) HandleRows(w http.ResponseWriter, r *http.Request) {
	job, tenantID, ok := h.job(w, r)
	if !ok {
		return
	}
	ids, err := parseIDs(r.URL.Query().Get("segment_ids"))
	if err != nil || len(ids) == 0 || len(ids) > MaxRows {
		httputil.BadRequest(w, "segment_ids must be 1 to 500 comma-separated ids")
		return
	}
	segments, err := h.queries.ListEditorSegmentsByIDs(r.Context(), db.ListEditorSegmentsByIDsParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID, Ids: ids,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "segments")
		return
	}
	found := make([]int64, len(segments))
	for i, s := range segments {
		found[i] = s.ID
	}
	translations, err := h.queries.ListEditorTranslationsBySegments(r.Context(), db.ListEditorTranslationsBySegmentsParams{
		SegmentIds: found, TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "translations")
		return
	}
	bySegment := make(map[int64]*db.Translation, len(translations))
	for i := range translations {
		bySegment[translations[i].SegmentID] = &translations[i]
	}
	translationIDs := make([]int64, len(translations))
	for i, t := range translations {
		translationIDs[i] = t.ID
	}
	previous, err := h.queries.ListEditorPreviousStepTexts(r.Context(), db.ListEditorPreviousStepTextsParams{
		TranslationIds: translationIDs, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "previous step texts")
		return
	}
	baseline := make(map[int64]*string, len(previous))
	for i := range previous {
		baseline[previous[i].TranslationID] = &previous[i].Text
	}
	sourceLanguage, err := h.queries.GetEditorSourceLanguage(r.Context(), db.GetEditorSourceLanguageParams{
		FileID: job.FileID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServiceError(w, err, "load", "source language")
		return
	}
	out := make([]Row, len(segments))
	for i, s := range segments {
		row := Row{Segment: s, Translation: bySegment[s.ID], Placeables: placeable.Find(s.SourceText, sourceLanguage)}
		if row.Translation != nil {
			row.PreviousStepText = baseline[row.Translation.ID]
		}
		out[i] = row
	}
	httputil.Data(w, out)
}

// HandleSearch answers GET /api/jobs/{jobID}/editor/search?q=&source=&target=&case=&regex=.
// Without regex the text is trimmed; source and target default to on.
func (h *Handler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	job, tenantID, ok := h.job(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	opts := SearchOptions{
		Text:          q.Get("q"),
		Source:        q.Get("source") != "0",
		Target:        q.Get("target") != "0",
		CaseSensitive: q.Get("case") == "1",
		Regex:         q.Get("regex") == "1",
	}
	if strings.TrimSpace(opts.Text) == "" {
		httputil.BadRequest(w, "q is required")
		return
	}
	match, err := opts.Matcher()
	if err != nil {
		httputil.BadRequest(w, "invalid regular expression")
		return
	}
	rows, err := h.queries.ListEditorTexts(r.Context(), db.ListEditorTextsParams{
		TargetLanguage: job.TargetLanguage, FileID: job.FileID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServiceError(w, err, "search", "segments")
		return
	}
	res := SearchResult{SegmentIDs: []int64{}}
	for _, row := range rows {
		if match(row.SourceText, row.TargetText) {
			res.SegmentIDs = append(res.SegmentIDs, row.SegmentID)
		}
	}
	httputil.Data(w, res)
}

// HandleRefs answers GET /api/jobs/{jobID}/editor/refs: every segment a
// cross-reference chip in the file points at, with its source and target.
func (h *Handler) HandleRefs(w http.ResponseWriter, r *http.Request) {
	job, tenantID, ok := h.job(w, r)
	if !ok {
		return
	}
	sources, err := h.queries.ListEditorRefSources(r.Context(), db.ListEditorRefSourcesParams{
		TargetLanguage: job.TargetLanguage, FileID: job.FileID, TenantID: tenantID,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "references")
		return
	}
	keys := refKeys(sources)
	out := []Ref{}
	if len(keys) > 0 {
		rows, err := h.queries.ListEditorSegmentsByKeys(r.Context(), db.ListEditorSegmentsByKeysParams{
			TargetLanguage: job.TargetLanguage, FileID: job.FileID, TenantID: tenantID, Keys: keys,
		})
		if err != nil {
			httputil.ServiceError(w, err, "list", "references")
			return
		}
		// A key split in this language points at its first piece.
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			if seen[row.SegmentKey] {
				continue
			}
			seen[row.SegmentKey] = true
			out = append(out, Ref{Key: row.SegmentKey, SegmentID: row.SegmentID, Source: row.SourceText, Target: row.TargetText})
		}
	}
	httputil.Data(w, out)
}

// HandleWordCandidates answers GET /api/jobs/{jobID}/editor/word?word=: the
// translations whose target contains the word anywhere. The caller narrows
// them to whole-word matches.
func (h *Handler) HandleWordCandidates(w http.ResponseWriter, r *http.Request) {
	job, tenantID, ok := h.job(w, r)
	if !ok {
		return
	}
	word := r.URL.Query().Get("word")
	if word == "" || len(word) > maxWordLength {
		httputil.BadRequest(w, "word must be 1 to 200 characters")
		return
	}
	rows, err := h.queries.ListEditorWordCandidates(r.Context(), db.ListEditorWordCandidatesParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage, TenantID: tenantID, Word: word,
	})
	if err != nil {
		httputil.ServiceError(w, err, "list", "translations")
		return
	}
	out := make([]WordCandidate, len(rows))
	for i, row := range rows {
		out[i] = WordCandidate{
			ID: row.ID, SegmentID: row.SegmentID, Locked: row.Locked,
			ActiveText: row.ActiveText, ActiveRichText: row.ActiveRichText,
			UpdatedAt: row.UpdatedAt,
		}
	}
	httputil.Data(w, out)
}

func parseIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// SearchOptions mirror the CAT editor's filter-bar text search.
type SearchOptions struct {
	Text          string
	Source        bool
	Target        bool
	CaseSensitive bool
	Regex         bool
}

// Matcher compiles the search once. Without regex the text is trimmed and
// matched as a substring.
func (o SearchOptions) Matcher() (func(source, target string) bool, error) {
	if !o.Source && !o.Target {
		return func(string, string) bool { return false }, nil
	}
	var match func(string) bool
	switch {
	case o.Regex:
		pattern := o.Text
		if !o.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		match = re.MatchString
	case o.CaseSensitive:
		needle := strings.TrimSpace(o.Text)
		match = func(s string) bool { return strings.Contains(s, needle) }
	default:
		needle := strings.ToLower(strings.TrimSpace(o.Text))
		match = func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
	}
	return func(source, target string) bool {
		return (o.Source && match(source)) || (o.Target && match(target))
	}, nil
}
