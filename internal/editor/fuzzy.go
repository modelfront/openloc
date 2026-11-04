package editor

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	tm "openloc/internal/translationmemory"
)

const (
	// fuzzyCandidateLimit caps the trigram candidates one lookup rescores.
	fuzzyCandidateLimit = 30
	// maxFuzzyMatches caps the matches one lookup returns.
	maxFuzzyMatches = 5
)

// HandleFuzzy answers GET …/editor/fuzzy?segment_id=: the internal fuzzy
// matches of a segment of the scope, best first, at or above the project's
// fuzzy threshold. Identical sources are repetitions, which propagation
// fills, so they are not listed.
func (h *Handler) HandleFuzzy(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.scope(w, r)
	if !ok {
		return
	}
	segmentID, err := strconv.ParseInt(r.URL.Query().Get("segment_id"), 10, 64)
	if err != nil {
		httputil.BadRequest(w, "segment_id is required")
		return
	}
	ctx := r.Context()
	segs, err := h.queries.ListEditorSegmentsByIDs(ctx, db.ListEditorSegmentsByIDsParams{
		FileIds: scope.FileIDs(), TargetLanguage: scope.TargetLanguage, TenantID: scope.TenantID,
		Ids: []int64{segmentID},
	})
	if err != nil {
		httputil.ServiceError(w, err, "load", "segment")
		return
	}
	if len(segs) == 0 {
		httputil.NotFound(w, "segment")
		return
	}
	query := segs[0].SourceText
	threshold, err := h.fuzzyThreshold(ctx, scope)
	if err != nil {
		httputil.ServiceError(w, err, "load", "fuzzy threshold")
		return
	}
	candidates, err := h.fuzzyCandidates(ctx, db.ListEditorFuzzyCandidatesParams{
		TargetLanguage: scope.TargetLanguage, FileIds: scope.FileIDs(), TenantID: scope.TenantID,
		SegmentID: segmentID, Query: query, CandidateLimit: fuzzyCandidateLimit,
	})
	if err != nil {
		httputil.ServiceError(w, err, "find", "internal fuzzy matches")
		return
	}
	httputil.Data(w, rankFuzzy(query, threshold, candidates))
}

// rankFuzzy rescores candidates with the TM scorer, keeps those at or above
// threshold, best first (ties by segment id), at most maxFuzzyMatches.
func rankFuzzy(query string, threshold float32, candidates []db.ListEditorFuzzyCandidatesRow) []FuzzyMatch {
	type scored struct {
		m     FuzzyMatch
		score float64
	}
	var kept []scored
	for _, c := range candidates {
		score, ok := tm.LevenshteinScorer{}.Score(query, c.SourceText)
		if !ok || score < float64(threshold) {
			continue
		}
		kept = append(kept, scored{FuzzyMatch{
			SegmentID: c.SegmentID, FileID: c.FileID, TranslationID: c.TranslationID,
			Percent:    tm.ScoreToPercent(score),
			SourceText: c.SourceText, SourceRichText: c.SourceRichText,
			TargetText: c.TargetText, TargetRichText: c.TargetRichText,
			UpdatedAt: c.UpdatedAt.Time,
		}, score})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].score != kept[j].score {
			return kept[i].score > kept[j].score
		}
		return kept[i].m.SegmentID < kept[j].m.SegmentID
	})
	out := make([]FuzzyMatch, 0, min(len(kept), maxFuzzyMatches))
	for i := 0; i < len(kept) && i < maxFuzzyMatches; i++ {
		out = append(out, kept[i].m)
	}
	return out
}

// fuzzyThreshold is the project's TM fuzzy threshold, the bar a TM match
// clears to show in the Resources panel.
func (h *Handler) fuzzyThreshold(ctx context.Context, scope Scope) (float32, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: scope.ProjectID, TenantID: scope.TenantID})
	if err != nil {
		return 0, fmt.Errorf("load project: %w", err)
	}
	settings, err := h.queries.GetTMScopeSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return 0, fmt.Errorf("load TM settings: %w", err)
	}
	return tm.FuzzyThreshold(settings.FuzzyThreshold), nil
}

// fuzzyCandidates runs ListEditorFuzzyCandidates with pg_trgm's threshold set
// to the TM's low recall floor, so short segments still find candidates the
// rescore then judges.
func (h *Handler) fuzzyCandidates(ctx context.Context, arg db.ListEditorFuzzyCandidatesParams) ([]db.ListEditorFuzzyCandidatesRow, error) {
	var rows []db.ListEditorFuzzyCandidatesRow
	err := database.InTx(ctx, h.pool, func(_ pgx.Tx, q db.Querier) error {
		if err := q.SetTrgmSimilarityThreshold(ctx, tm.FindRecallThreshold); err != nil {
			return fmt.Errorf("set similarity threshold: %w", err)
		}
		var err error
		rows, err = q.ListEditorFuzzyCandidates(ctx, arg)
		return err
	})
	return rows, err
}
