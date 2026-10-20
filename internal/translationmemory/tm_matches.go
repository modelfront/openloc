package translationmemory

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	settingspkg "openloc/internal/settings"
)

// JobTMMatch is one fuzzy TM hit for a job's source segment.
type JobTMMatch struct {
	Score          float64 `json:"score"`
	SourceText     string  `json:"source_text"`
	TargetText     string  `json:"target_text"`
	SourceRichText *string `json:"source_rich_text"`
	TargetRichText *string `json:"target_rich_text"`
	TMName         string  `json:"tm_name"`
	TMID           int64   `json:"tm_id"`
	// Percent is what the Match column shows for this entry after
	// pre-translation or an insert: 101 for an in-context exact match.
	Percent int16 `json:"percent"`
	// TMEntryID is the matched entry; the panel's insert token carries it.
	TMEntryID int64 `json:"-"`
}

// errAllTMsFailed: every TM lookup errored — reported instead of a deceptive
// empty list, so "the DB is broken" is not read as "no matches".
var errAllTMsFailed = errors.New("every TM lookup failed")

// JobTMMatches returns the best fuzzy TM matches for sourceText across the TMs
// assigned to the job's project for its language pair, scored through the
// same HitScore / HitPercent as pre-translation. translationID names the
// segment looked up for (0: none), which lets an exact match score 101% in
// context. The job, file and project are resolved tenant-scoped;
// pgx.ErrNoRows when any is outside the tenant.
func (h *Handler) JobTMMatches(ctx context.Context, tenantID, jobID int64, sourceText string, translationID int64) ([]JobTMMatch, error) {
	job, err := h.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	file, err := h.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
	if err != nil {
		return nil, err
	}

	tmAssignments, err := h.queries.LookupSettingsTMs(ctx, db.LookupSettingsTMsParams{
		SettingsProfileID: project.SettingsProfileID,
		SourceLanguage:    project.SourceLanguage,
		TargetLanguage:    job.TargetLanguage,
	})
	if err != nil || len(tmAssignments) == 0 {
		return []JobTMMatch{}, nil
	}

	tmIDs := make([]int64, len(tmAssignments))
	for i, a := range tmAssignments {
		tmIDs[i] = a.TmID
	}
	tmNames := make(map[int64]string)
	for _, id := range tmIDs {
		coll, err := h.queries.GetTMCollection(ctx, db.GetTMCollectionParams{ID: id, TenantID: tenantID})
		if err == nil {
			tmNames[id] = coll.Name
		}
	}

	// TM-scope row (the linked TM profile's settings when the project follows
	// one), so the sidebar scores exactly like the pipeline. On a failed read
	// fall back to the default penalties rather than to none — dropping them
	// would over-report every variant match as clean.
	settings, err := h.queries.GetTMScopeSettingsProfile(ctx, project.SettingsProfileID)
	threshold := DefaultFuzzyThreshold
	penalties := DefaultPenalties()
	byTMOrder := false
	if err == nil {
		byTMOrder = settings.TmLookupOrder == settingspkg.TMLookupOrderTMOrder
		threshold = FuzzyThreshold(settings.FuzzyThreshold)
		penalties = PenaltiesFromProfile(settings)
	}
	penalties = penalties.WithTMs(tmAssignments)
	segment := h.segmentContext(ctx, tenantID, job, sourceText, translationID)

	type dedupKey struct{ src, tgt string }
	matches := []JobTMMatch{}
	seen := make(map[dedupKey]bool)
	failed := 0
	for _, tmID := range tmIDs {
		candidates, err := h.fuzzyLookup(ctx, db.FuzzyLookupTMParams{
			TmID:           tmID,
			SourceLanguage: project.SourceLanguage,
			TargetLanguage: job.TargetLanguage,
			Query:          sourceText,
			Threshold:      FindRecallThreshold,
			EntryLimit:     matchesPerTMCandidateLimit,
		})
		if err != nil {
			logging.Warn(ctx, err, "TM fuzzy lookup failed", "tm-fuzzy-lookup-failed", logging.TMID(tmID))
			failed++
			continue
		}
		for _, hit := range candidates {
			key := dedupKey{hit.SourceText, hit.TargetText}
			if seen[key] {
				continue
			}
			score := HitScore(h.scorer, penalties, sourceText, tmID, project.SourceLanguage, job.TargetLanguage, hit)
			if score < float64(threshold) {
				continue
			}
			seen[key] = true
			matches = append(matches, JobTMMatch{
				Score:          score,
				SourceText:     hit.SourceText,
				TargetText:     hit.TargetText,
				SourceRichText: hit.SourceRichText,
				TargetRichText: hit.TargetRichText,
				TMName:         tmNames[tmID],
				TMID:           tmID,
				TMEntryID:      hit.ID,
				Percent:        HitPercent(sourceText, hit, score, segment),
			})
		}
	}
	if failed == len(tmIDs) && failed > 0 {
		return nil, errAllTMsFailed
	}
	// Candidates were appended TM by TM in priority order, so a stable sort on
	// percent then score keeps ties in TM order (and a 101% above a 100%).
	// Under tm_order the TM position comes first, so the top row is the one
	// pre-translation would have picked.
	tmRank := make(map[int64]int, len(tmIDs))
	for i, id := range tmIDs {
		tmRank[id] = i
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if byTMOrder && matches[i].TMID != matches[j].TMID {
			return tmRank[matches[i].TMID] < tmRank[matches[j].TMID]
		}
		if matches[i].Percent != matches[j].Percent {
			return matches[i].Percent > matches[j].Percent
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > maxMatchesInResponse {
		matches = matches[:maxMatchesInResponse]
	}
	return matches, nil
}

// segmentContext is the neighbour context of the segment the panel looks up
// for, or nil (no 101%) when translationID is 0, belongs to another job, or
// its segment's source is not sourceText.
func (h *Handler) segmentContext(ctx context.Context, tenantID int64, job db.Job, sourceText string, translationID int64) *NeighborContext {
	if translationID <= 0 {
		return nil
	}
	t, err := h.queries.GetTranslationInFileTenant(ctx, db.GetTranslationInFileTenantParams{
		ID: translationID, FileID: job.FileID, TenantID: tenantID,
	})
	if err != nil || t.TargetLanguage != job.TargetLanguage {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			logging.Warn(ctx, err, "TM matches: segment lookup failed, no in-context match", "tm-matches-context-failed", logging.TranslationID(translationID))
		}
		return nil
	}
	seg, err := h.queries.GetSegment(ctx, t.SegmentID)
	if err != nil {
		logging.Warn(ctx, err, "TM matches: segment source lookup failed, no in-context match", "tm-matches-context-failed", logging.TranslationID(translationID))
		return nil
	}
	if seg.SourceText != sourceText {
		return nil
	}
	nb, err := h.queries.GetSegmentNeighborSources(ctx, t.SegmentID)
	if err != nil {
		logging.Warn(ctx, err, "TM matches: neighbour lookup failed, no in-context match", "tm-matches-context-failed", logging.TranslationID(translationID))
		return nil
	}
	nc := NewNeighborContext(nb.PrevSource, nb.NextSource)
	return &nc
}
