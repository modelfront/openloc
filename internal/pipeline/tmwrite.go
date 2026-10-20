package pipeline

import (
	"context"
	"fmt"
	"slices"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/timing"
	"openloc/internal/translationmemory"
)

// TMWriteResult is what SaveApprovedToTM found and wrote.
type TMWriteResult struct {
	ApprovedCount int  // human-approved translations for this file + target language
	TMSavedCount  int  // (translation × TM) write operations
	NoWriteTMs    bool // project has no matching write TM for the language pair
}

// tmWriteGroup is approved translations of one file and target language,
// written to the write TMs of one settings profile.
type tmWriteGroup struct {
	projectID, settingsProfileID, fileID int64
	sourceLang, targetLang               string
	rows                                 []tmEntryWrite
}

// SaveApprovedToTM writes every human-approved translation for a file+language
// to the project's write-mode TMs. Upserts are idempotent, so it's safe to
// re-run on each step completion + job Complete.
//
// A translation that did not reach every write TM makes it return an error,
// after trying all the others, so job Complete stays open for a retry instead
// of finishing with the TM write lost. A segment the database refuses for good
// (unstorableInTM) is logged and skipped, since no retry could store it.
//
// CRITICAL: only status='approved' (human-confirmed) rows are listed — never
// auto_approved / APE output (would degrade the TM). The returned
// ApprovedCount is how many such rows the file+language has; it's the number
// callers report as "approved" (see TMWriteResult).
func (s *Service) SaveApprovedToTM(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string) (result TMWriteResult, err error) {
	// Whole-operation span (n = 1 per call): the interesting figure is how
	// long populating the TM adds to job completion, not the per-upsert cost.
	ctx, done := timing.Start(ctx, timing.StageCompletionTMWrite)
	defer func() { done(err) }()

	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return TMWriteResult{}, fmt.Errorf("get file: %w", err)
	}
	project, err := s.queries.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		return TMWriteResult{}, fmt.Errorf("get project: %w", err)
	}

	rows, err := s.queries.ListApprovedForTM(ctx, db.ListApprovedForTMParams{
		FileID:         fileID,
		TargetLanguage: targetLang,
	})
	if err != nil {
		return TMWriteResult{}, fmt.Errorf("list approved for TM: %w", err)
	}
	g := tmWriteGroup{projectID: project.ID, settingsProfileID: project.SettingsProfileID, fileID: fileID, sourceLang: sourceLang, targetLang: targetLang}
	for _, row := range rows {
		if row.ActiveText == nil {
			continue
		}
		g.rows = append(g.rows, newTMEntryWrite(row.ID, row.SegmentID, row.SourceText, *row.ActiveText, row.SourceRichText, row.ActiveRichText))
	}
	result, err = s.writeGroupsToTM(ctx, []tmWriteGroup{g}, TMReasonCompletionBulk)
	result.ApprovedCount = len(rows)
	return result, err
}

// SaveTranslationsToTM writes the listed translations of the tenant to their
// projects' write-mode TMs, as SaveApprovedToTM does for a file: only
// human-approved rows (status approved, confirmed by a user, never
// auto_approved or assistant-confirmed) are written, the others are ignored.
// Each entry carries its document context, an overwritten target goes to
// translation_memory_history under reason (TMReasonCATConfirm or
// TMReasonCompletionBulk), and updated_by is the user in ctx. The statements
// grow with the files, languages and chunks of 200, not with the rows.
// Returns the (translation × TM) writes done; an error after trying every
// row when one did not reach every write TM.
func (s *Service) SaveTranslationsToTM(ctx context.Context, tenantID int64, translationIDs []int64, reason string) (int, error) {
	if len(translationIDs) == 0 {
		return 0, nil
	}
	rows, err := s.queries.ListApprovedForTMByIDs(ctx, db.ListApprovedForTMByIDsParams{Ids: translationIDs, TenantID: tenantID})
	if err != nil {
		return 0, fmt.Errorf("list approved for TM: %w", err)
	}
	// Rows come ordered by file and target language, in document order.
	var groups []tmWriteGroup
	for _, row := range rows {
		if row.ActiveText == nil {
			continue
		}
		if n := len(groups); n == 0 || groups[n-1].fileID != row.FileID || groups[n-1].targetLang != row.TargetLanguage {
			groups = append(groups, tmWriteGroup{
				projectID: row.ProjectID, settingsProfileID: row.SettingsProfileID, fileID: row.FileID,
				sourceLang: row.ProjectSourceLanguage, targetLang: row.TargetLanguage,
			})
		}
		g := &groups[len(groups)-1]
		g.rows = append(g.rows, newTMEntryWrite(row.ID, row.SegmentID, row.SourceText, *row.ActiveText, row.SourceRichText, row.ActiveRichText))
	}
	res, err := s.writeGroupsToTM(ctx, groups, reason)
	return res.TMSavedCount, err
}

func newTMEntryWrite(translationID, segmentID int64, source, target string, sourceRich, targetRich *string) tmEntryWrite {
	// nil rich text → "" → NULL via NULLIF in the SQL upsert.
	w := tmEntryWrite{translationID: translationID, segmentID: segmentID, source: source, target: target}
	if sourceRich != nil {
		w.sourceRich = *sourceRich
	}
	if targetRich != nil {
		w.targetRich = *targetRich
	}
	return w
}

// writeGroupsToTM writes each group to its write TMs in chunks. The acting
// user is resolved once, the write TMs once per group, and the neighbour
// hashes of the written segments in one read, so the same context can later
// leverage an entry as a 101% in-context exact match (a failed read stores
// none).
func (s *Service) writeGroupsToTM(ctx context.Context, groups []tmWriteGroup, reason string) (TMWriteResult, error) {
	var result TMWriteResult
	changedBy := userIDFromCtx(ctx)
	updatedBy := ""
	neighbors := s.neighborHashesFor(ctx, groups)
	failed := make(map[int64]bool)
	total := 0
	for _, g := range groups {
		if len(g.rows) == 0 {
			continue
		}
		total += len(g.rows)
		writeTMs, err := s.queries.LookupSettingsWriteTMs(ctx, db.LookupSettingsWriteTMsParams{
			SettingsProfileID: g.settingsProfileID,
			SourceLanguage:    g.sourceLang,
			TargetLanguage:    g.targetLang,
		})
		if err != nil {
			return result, fmt.Errorf("lookup write TMs: %w", err)
		}
		if len(writeTMs) == 0 {
			result.NoWriteTMs = true
			logging.Warn(ctx, nil, "no write TM for the language pair; approved segments not saved to TM", "tm-write-bulk-no-write-tm",
				append(logging.Langs(g.sourceLang, g.targetLang), logging.ProjectID(g.projectID), logging.Count(len(g.rows)))...)
			continue
		}
		if updatedBy == "" {
			updatedBy = s.updatedByLabel(ctx, changedBy)
		}
		for i := range g.rows {
			if c, ok := neighbors[g.rows[i].segmentID]; ok {
				g.rows[i].prevHash, g.rows[i].nextHash = c.Prev, c.Next
			}
		}
		for _, tmID := range writeTMs {
			for chunk := range slices.Chunk(g.rows, tmWriteChunk) {
				saved, refused := s.upsertTMChunk(ctx, tmID, g.sourceLang, g.targetLang, updatedBy, changedBy, reason, chunk)
				// TMSavedCount is the count of (translation × TM) write
				// operations, not the count of distinct TMs touched; the job
				// Complete response relies on that meaning.
				result.TMSavedCount += saved
				for _, f := range refused {
					if unstorableInTM(f.err) {
						logging.Error(ctx, f.err, "TM cannot store this translation; skipped", "tm-write-unstorable",
							logging.TranslationID(f.translationID), logging.TMID(tmID))
						continue
					}
					logging.Error(ctx, f.err, "failed to upsert TM entry", "tm-write-upsert-failed",
						logging.TranslationID(f.translationID), logging.TMID(tmID))
					failed[f.translationID] = true
				}
			}
		}
	}
	if len(failed) > 0 {
		return result, fmt.Errorf("%d of %d approved translations were not saved to every write TM", len(failed), total)
	}
	return result, nil
}

// neighborHashesFor returns the neighbour-context hashes of the groups'
// segments by segment id, read in one statement; nil when the read fails.
func (s *Service) neighborHashesFor(ctx context.Context, groups []tmWriteGroup) map[int64]translationmemory.NeighborContext {
	var ids []int64
	for _, g := range groups {
		for _, r := range g.rows {
			ids = append(ids, r.segmentID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.queries.ListSegmentNeighborSourcesByIDs(ctx, ids)
	if err != nil {
		logging.Warn(ctx, err, "neighbor lookup failed; storing no context", "tm-write-neighbor-lookup-failed", logging.Count(len(ids)))
		return nil
	}
	out := make(map[int64]translationmemory.NeighborContext, len(rows))
	for _, r := range rows {
		out[r.ID] = translationmemory.NewNeighborContext(r.PrevSource, r.NextSource)
	}
	return out
}
