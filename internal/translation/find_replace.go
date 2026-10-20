package translation

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/inlinetag"
	"openloc/internal/segmentbulk"
)

// Find-and-replace modes.
const (
	FindModeFind       = "find"
	FindModeReplaceOne = "replace_one"
	FindModeReplaceAll = "replace_all"
)

// FindReplaceParams is one find-and-replace request on a job.
type FindReplaceParams struct {
	Find          string
	Replace       string
	CaseSensitive bool
	Mode          string // FindMode*; "" means replace_all
	TranslationID int64  // required for replace_one
	// TranslationIDs narrows the search to these rows of the job — the
	// editor's selection. Nil searches the whole job.
	TranslationIDs []int64
	// IncludeLocked adds locked segments. Find lists them for anyone; Replace
	// writes them only for owner/admin, which unlocks them (a draft is never
	// locked), and counts them as Locked for everyone else.
	IncludeLocked bool
}

// FindMatch is one segment the search text occurs in.
type FindMatch struct {
	TranslationID int64
	Count         int
	OldText       string
	NewText       string
	Locked        bool
}

// FindReplaceResult reports the matches (find) or what a replace wrote.
type FindReplaceResult struct {
	Matches  int
	Segments int
	Found    []FindMatch
	// Replace only: occurrences replaced, and the matching segments left alone.
	Replaced int
	// Locked: matching locked segments that were not written.
	Locked int
	// Held: matching segments another user has open, not written.
	Held int
	// SkippedTags: the match crosses an inline tag (or sits inside a
	// placeholder), so the tagged form cannot be edited without breaking it.
	SkippedTags int
	// Conflicts: the segment changed since it was read.
	Conflicts int
	// Rejected: the replacement would leave the segment empty.
	Rejected int
	// Failed: the write itself failed (logged); the segment is unchanged.
	Failed       int
	TaskReopened bool
}

// frRow is a job row as find-and-replace reads it.
type frRow struct {
	id        int64
	locked    bool
	text      *string
	rich      *string
	updatedAt pgtype.Timestamptz
}

// FindReplace finds p.Find in the job's translations and, unless the mode is
// find, replaces it. A replace is an edit like any other: it saves drafts
// through BatchDraft (status needs_review, the user as editor, optimistic
// lock, task guard) and changes only the text runs of the tagged form so
// inline tags survive. Locked segments are left out unless p.IncludeLocked,
// and written only by owner/admin; a written locked segment is unlocked.
// Segments another user holds are counted in Held and never written.
func (s *Service) FindReplace(ctx context.Context, tenantID, userID int64, job db.Job, p FindReplaceParams) (FindReplaceResult, error) {
	if p.Find == "" {
		return FindReplaceResult{}, httputil.NewClientError("find is required")
	}
	if p.Mode == "" {
		p.Mode = FindModeReplaceAll
	}
	if p.Mode != FindModeFind && p.Mode != FindModeReplaceOne && p.Mode != FindModeReplaceAll {
		return FindReplaceResult{}, httputil.NewClientError("mode must be 'find', 'replace_one', or 'replace_all'")
	}
	if p.Mode == FindModeReplaceOne && p.TranslationID == 0 {
		return FindReplaceResult{}, httputil.NewClientError("translation_id is required for replace_one")
	}
	rows, err := s.findReplaceRows(ctx, job, p.TranslationIDs)
	if err != nil {
		return FindReplaceResult{}, err
	}

	writeLocked := p.IncludeLocked && MayWriteLocked(ctx)

	var res FindReplaceResult
	byID := make(map[int64]frRow, len(rows))
	for _, r := range rows {
		if r.text == nil || *r.text == "" || (r.locked && !p.IncludeLocked) {
			continue
		}
		count, newText := inlinetag.ReplaceText(*r.text, p.Find, p.Replace, p.CaseSensitive)
		if count == 0 {
			continue
		}
		byID[r.id] = r
		res.Found = append(res.Found, FindMatch{TranslationID: r.id, Count: count, OldText: *r.text, NewText: newText, Locked: r.locked})
		res.Matches += count
	}
	res.Segments = len(res.Found)
	if p.Mode == FindModeFind {
		return res, nil
	}

	var fixes []DraftFix
	counts := map[int64]int{}
	for _, m := range res.Found {
		if p.Mode == FindModeReplaceOne && m.TranslationID != p.TranslationID {
			continue
		}
		r := byID[m.TranslationID]
		switch {
		case r.locked && !writeLocked:
			res.Locked++
			continue
		case m.NewText == "":
			res.Rejected++
			continue
		}
		var newRich *string
		if r.rich != nil {
			n, out := inlinetag.ReplaceInRich(*r.rich, p.Find, p.Replace, p.CaseSensitive)
			if n != m.Count {
				res.SkippedTags++
				continue
			}
			newRich = &out
		}
		counts[m.TranslationID] = m.Count
		fixes = append(fixes, DraftFix{TranslationID: m.TranslationID, HumanText: m.NewText, HumanRichText: newRich, ExpectedUpdatedAt: r.updatedAt})
	}

	res.Segments = 0
	// BatchDraft takes at most MaxBatchDraftFixes rows per call.
	for part := range slices.Chunk(fixes, MaxBatchDraftFixes) {
		written, err := s.BatchDraft(ctx, tenantID, userID, job.FileID, part, writeLocked)
		if err != nil {
			return FindReplaceResult{}, err
		}
		for _, t := range written.Applied {
			res.Replaced += counts[t.ID]
		}
		res.Segments += len(written.Applied)
		res.Locked += len(written.Locked)
		res.Held += len(written.Held)
		res.Conflicts += len(written.Conflicts)
		// Empty replacements never reach BatchDraft, so its rejections are
		// failed writes.
		res.Failed += len(written.Rejected)
		res.TaskReopened = res.TaskReopened || written.TaskReopened
	}
	return res, nil
}

// MayWriteLocked reports whether find and replace may write a locked segment
// for the caller: owner and admin.
func MayWriteLocked(ctx context.Context) bool {
	claims, ok := auth.ClaimsFromContext(ctx)
	return ok && auth.IsAdminRole(claims.Role)
}

// findReplaceRows reads the job's rows in document order, or the given rows
// of the job in request order.
func (s *Service) findReplaceRows(ctx context.Context, job db.Job, ids []int64) ([]frRow, error) {
	if ids != nil {
		ids, err := segmentbulk.NormalizeIDs(ids)
		if err != nil {
			return nil, err
		}
		byID, err := segmentbulk.JobRows(ctx, s.queries, job, auth.UserIDFromContext(ctx), ids)
		if err != nil {
			return nil, err
		}
		out := make([]frRow, 0, len(byID))
		for _, id := range ids {
			if r, ok := byID[id]; ok {
				out = append(out, frRow{id: r.ID, locked: r.Locked, text: r.ActiveText, rich: r.ActiveRichText, updatedAt: r.UpdatedAt})
			}
		}
		return out, nil
	}
	all, err := s.queries.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID: job.FileID, TargetLanguage: job.TargetLanguage,
	})
	if err != nil {
		return nil, fmt.Errorf("load translations: %w", err)
	}
	out := make([]frRow, 0, len(all))
	for _, t := range all {
		out = append(out, frRow{id: t.ID, locked: t.Locked, text: t.ActiveText, rich: t.ActiveRichText, updatedAt: t.UpdatedAt})
	}
	return out, nil
}
