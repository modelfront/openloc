package translationmemory

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/inlinetag"
)

const (
	// frPreviewDefaultLimit and frPreviewMaxLimit bound one preview page.
	frPreviewDefaultLimit = 100
	frPreviewMaxLimit     = 500
	// frApplyChunk is how many entries replace-all reads, snapshots and
	// updates per statement.
	frApplyChunk = 1000
)

// FindReplaceQuery is one bulk find-and-replace over a TM. Scope is "source",
// "target" or "both"; empty language filters match any language.
type FindReplaceQuery struct {
	TMID           int64
	Find           string
	Replace        string
	CaseSensitive  bool
	Scope          string
	SourceLanguage string
	TargetLanguage string
}

// FindReplaceMatch is one entry the find term occurs in, with its text after
// the replacement.
type FindReplaceMatch struct {
	EntryID          int64  `json:"entry_id"`
	SourceText       string `json:"source_text"`
	TargetText       string `json:"target_text"`
	NewSourceText    string `json:"new_source_text"`
	NewTargetText    string `json:"new_target_text"`
	SourceMatchCount int    `json:"source_match_count"`
	TargetMatchCount int    `json:"target_match_count"`

	before db.TranslationMemory
}

// FindReplacePreview is one page of matches plus the totals over the TM.
// NextCursor is nil on the last page.
type FindReplacePreview struct {
	Matches           []FindReplaceMatch `json:"matches"`
	TotalEntries      int64              `json:"total_entries"`
	TotalReplacements int64              `json:"total_replacements"`
	NextCursor        *int64             `json:"next_cursor"`
}

func (fq FindReplaceQuery) inSource() bool { return fq.Scope == "source" || fq.Scope == "both" }
func (fq FindReplaceQuery) inTarget() bool { return fq.Scope == "target" || fq.Scope == "both" }

// likePattern matches values containing s, with LIKE's wildcards escaped.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

func (fq FindReplaceQuery) page(afterID int64, limit int32) db.FindTMEntriesForFRParams {
	return db.FindTMEntriesForFRParams{
		TmID: fq.TMID, SourceLanguage: fq.SourceLanguage, TargetLanguage: fq.TargetLanguage,
		AfterID: afterID, InSource: fq.inSource(), InTarget: fq.inTarget(),
		CaseSensitive: fq.CaseSensitive, Pattern: likePattern(fq.Find), PageLimit: limit,
	}
}

// matches computes the replacement for each entry. The SQL filter only
// narrows the candidates; inlinetag.ReplaceText decides what is replaced.
func (fq FindReplaceQuery) matches(entries []db.TranslationMemory) []FindReplaceMatch {
	out := make([]FindReplaceMatch, 0, len(entries))
	for _, e := range entries {
		m := FindReplaceMatch{EntryID: e.ID, SourceText: e.SourceText, TargetText: e.TargetText,
			NewSourceText: e.SourceText, NewTargetText: e.TargetText, before: e}
		if fq.inSource() {
			m.SourceMatchCount, m.NewSourceText = inlinetag.ReplaceText(e.SourceText, fq.Find, fq.Replace, fq.CaseSensitive)
		}
		if fq.inTarget() {
			m.TargetMatchCount, m.NewTargetText = inlinetag.ReplaceText(e.TargetText, fq.Find, fq.Replace, fq.CaseSensitive)
		}
		if m.SourceMatchCount+m.TargetMatchCount > 0 {
			out = append(out, m)
		}
	}
	return out
}

// planWithValues makes the transaction plan each statement with its values.
// The find-and-replace filters switch on parameters (scope, case), so only a
// plan that sees the values can use the trigram indexes; Postgres' generic
// plan for a prepared statement reads the whole TM, and it is picked after a
// few runs wherever most TMs are small.
func planWithValues(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = force_custom_plan"); err != nil {
		return fmt.Errorf("set plan cache mode: %w", err)
	}
	return nil
}

// PreviewFindReplace returns the page of matches after the entry id cursor
// (0: from the start) and the totals over the whole TM. The caller has
// checked the TM belongs to the tenant.
func (s *Service) PreviewFindReplace(ctx context.Context, fq FindReplaceQuery, cursor int64, limit int) (FindReplacePreview, error) {
	if limit <= 0 {
		limit = frPreviewDefaultLimit
	}
	limit = min(limit, frPreviewMaxLimit)
	var entries []db.TranslationMemory
	var totals db.CountTMFRMatchesRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := planWithValues(ctx, tx); err != nil {
			return err
		}
		q := db.New(tx)
		var err error
		if entries, err = q.FindTMEntriesForFR(ctx, fq.page(cursor, int32(limit))); err != nil {
			return fmt.Errorf("find TM entries: %w", err)
		}
		totals, err = q.CountTMFRMatches(ctx, db.CountTMFRMatchesParams{
			TmID: fq.TMID, SourceLanguage: fq.SourceLanguage, TargetLanguage: fq.TargetLanguage,
			InSource: fq.inSource(), InTarget: fq.inTarget(), CaseSensitive: fq.CaseSensitive,
			Find: fq.Find, Pattern: likePattern(fq.Find),
		})
		if err != nil {
			return fmt.Errorf("count TM matches: %w", err)
		}
		return nil
	})
	if err != nil {
		return FindReplacePreview{}, err
	}
	p := FindReplacePreview{Matches: fq.matches(entries), TotalEntries: totals.Entries, TotalReplacements: totals.Replacements}
	if len(entries) == limit {
		last := entries[len(entries)-1].ID
		p.NextCursor = &last
	}
	return p, nil
}

// ApplyFindReplace replaces the find term in every matching entry of the TM
// in one transaction: per chunk, one statement snapshots the entries to
// history and one updates them. An edited field loses its rich text, which
// would no longer match the plain text; an untouched one keeps it. Returns
// the entries updated and the occurrences replaced.
func (s *Service) ApplyFindReplace(ctx context.Context, fq FindReplaceQuery, userID int64, updatedBy string) (entries, replacements int, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := planWithValues(ctx, tx); err != nil {
			return err
		}
		q := db.New(tx)
		var cursor int64
		for {
			rows, err := q.FindTMEntriesForFR(ctx, fq.page(cursor, frApplyChunk))
			if err != nil {
				return fmt.Errorf("find TM entries: %w", err)
			}
			if len(rows) == 0 {
				return nil
			}
			cursor = rows[len(rows)-1].ID
			ms := fq.matches(rows)
			if len(ms) > 0 {
				if err := applyFRChunk(ctx, q, fq.TMID, ms, userID, updatedBy); err != nil {
					return err
				}
			}
			for _, m := range ms {
				replacements += m.SourceMatchCount + m.TargetMatchCount
			}
			entries += len(ms)
			if len(rows) < frApplyChunk {
				return nil
			}
		}
	})
	if err != nil {
		return 0, 0, err
	}
	return entries, replacements, nil
}

func applyFRChunk(ctx context.Context, q *db.Queries, tmID int64, ms []FindReplaceMatch, userID int64, updatedBy string) error {
	p := db.BulkUpdateTMEntryTextsParams{
		TmID: tmID, UpdatedBy: updatedBy,
		Ids:             make([]int64, len(ms)),
		SourceTexts:     make([]string, len(ms)),
		TargetTexts:     make([]string, len(ms)),
		SourceRichTexts: make([]string, len(ms)),
		TargetRichTexts: make([]string, len(ms)),
	}
	for i, m := range ms {
		p.Ids[i], p.SourceTexts[i], p.TargetTexts[i] = m.EntryID, m.NewSourceText, m.NewTargetText
		if m.SourceMatchCount == 0 && m.before.SourceRichText != nil {
			p.SourceRichTexts[i] = *m.before.SourceRichText
		}
		if m.TargetMatchCount == 0 && m.before.TargetRichText != nil {
			p.TargetRichTexts[i] = *m.before.TargetRichText
		}
	}
	if err := q.InsertTMHistoryFromEntries(ctx, db.InsertTMHistoryFromEntriesParams{
		TmID: tmID, Ids: p.Ids, ChangedByUserID: &userID, ChangeReason: "find_replace",
	}); err != nil {
		return fmt.Errorf("record TM history: %w", err)
	}
	if _, err := q.BulkUpdateTMEntryTexts(ctx, p); err != nil {
		return fmt.Errorf("update TM entries: %w", err)
	}
	return nil
}
