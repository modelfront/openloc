package qa

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"

	db "openloc/internal/database/sqlcgen"
)

// runPageSize is how many rows a job run checks and writes per batch.
const runPageSize = 500

// maxOtherTargets is how many other targets of the same source the
// consistency check sees, as ListOtherTargetsForSource returns them.
const maxOtherTargets = 10

// segmentLookups is what checkSegment reads beyond the segment itself.
type segmentLookups interface {
	concepts(ctx context.Context, source string) []ConceptForQA
	otherTargets(ctx context.Context, translationID int64, source string) []string
}

// dbLookups queries per segment: the single-segment paths. Both lookups are
// best-effort; a failed query yields no terms or no other targets.
type dbLookups struct {
	s  *Service
	sc segmentScope
}

func (l dbLookups) concepts(ctx context.Context, source string) []ConceptForQA {
	rows, err := l.s.queries.LookupSettingsTerms(ctx, db.LookupSettingsTermsParams{
		SettingsProfileID: l.sc.settingsProfileID,
		SourceLanguage:    l.sc.sourceLanguage,
		TargetLanguage:    l.sc.targetLanguage,
		SourceText:        source,
		ProjectDomain:     l.sc.projectDomain,
	})
	if err != nil {
		return nil
	}
	return ConceptsFromLookupRows(rows)
}

func (l dbLookups) otherTargets(ctx context.Context, translationID int64, source string) []string {
	others, err := l.s.queries.ListOtherTargetsForSource(ctx, db.ListOtherTargetsForSourceParams{
		FileID:               l.sc.fileID,
		TargetLanguage:       l.sc.targetLanguage,
		SourceText:           source,
		ExcludeTranslationID: translationID,
	})
	if err != nil {
		return nil
	}
	return others
}

// batchLookups answers from data loaded once per run (terms) and once per
// batch (other targets of the batch's sources, in translation id order).
type batchLookups struct {
	terms  *termIndex
	others map[string][]db.ListTargetsForSourcesRow
}

func (l batchLookups) concepts(_ context.Context, source string) []ConceptForQA {
	if l.terms == nil {
		return nil
	}
	return l.terms.concepts(source)
}

func (l batchLookups) otherTargets(_ context.Context, translationID int64, source string) []string {
	var out []string
	for _, r := range l.others[source] {
		if r.TranslationID == translationID {
			continue
		}
		out = append(out, r.TargetText)
		if len(out) == maxOtherTargets {
			break
		}
	}
	return out
}

// qaRow is one row a QA run checks: its stored source and target.
type qaRow struct {
	id                     int64
	source, target         string
	sourceRich, targetRich string
}

// loadRunTerms loads the glossary once for a run, when a rule needs it.
func (s *Service) loadRunTerms(ctx context.Context, js jobScope) (*termIndex, error) {
	if !js.rs.enabled("terminology") && !js.rs.enabled("forbidden_terms") {
		return nil, nil
	}
	rows, err := s.queries.ListSettingsTermsForQA(ctx, db.ListSettingsTermsForQAParams{
		SettingsProfileID: js.sc.settingsProfileID,
		SourceLanguage:    js.sc.sourceLanguage,
		TargetLanguage:    js.sc.targetLanguage,
		ProjectDomain:     js.sc.projectDomain,
	})
	if err != nil {
		return nil, fmt.Errorf("load terms: %w", err)
	}
	return newTermIndex(rows), nil
}

// runJobPages checks every row of the job, one page at a time.
func (s *Service) runJobPages(ctx context.Context, job db.Job, js jobScope) (JobRunResult, error) {
	terms, err := s.loadRunTerms(ctx, js)
	if err != nil {
		return JobRunResult{}, err
	}
	var result JobRunResult
	afterPos, afterSeg := int32(math.MinInt32), int64(0)
	for {
		page, err := s.queries.ListTranslationsForQAPage(ctx, db.ListTranslationsForQAPageParams{
			FileID: job.FileID, TargetLanguage: job.TargetLanguage,
			AfterPosition: afterPos, AfterSegmentID: afterSeg, PageSize: runPageSize,
		})
		if err != nil {
			return result, fmt.Errorf("list translations: %w", err)
		}
		if len(page) == 0 {
			return result, nil
		}
		rows := make([]qaRow, len(page))
		for i, p := range page {
			rows[i] = qaRow{id: p.TranslationID, source: p.SourceText, target: p.TargetText,
				sourceRich: p.SourceRichText, targetRich: p.TargetRichText}
		}
		n, err := s.runRows(ctx, js, terms, rows)
		result.Findings += n
		if err != nil {
			return result, err
		}
		last := page[len(page)-1]
		afterPos, afterSeg = last.Position, last.SegmentID
		if len(page) < runPageSize {
			return result, nil
		}
	}
}

// runRows checks one batch of the job's rows and replaces their findings in
// one transaction. A row with no findings now and none before is not written.
// It returns the number of findings stored.
func (s *Service) runRows(ctx context.Context, js jobScope, terms *termIndex, rows []qaRow) (int, error) {
	lk := batchLookups{terms: terms}
	if js.rs.enabled("consistency") {
		others, err := s.loadOtherTargets(ctx, js.sc, rows)
		if err != nil {
			return 0, err
		}
		lk.others = others
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	stored, err := s.queries.ListTranslationIDsWithQAResults(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("list stored QA results: %w", err)
	}
	hadFindings := make(map[int64]bool, len(stored))
	for _, id := range stored {
		hadFindings[id] = true
	}

	var w issueWrite
	findings := 0
	for _, r := range rows {
		issues := s.checkSegment(ctx, js.rs, lk, r.id, r.source, r.target, r.sourceRich, r.targetRich)
		if len(issues) == 0 && !hadFindings[r.id] {
			continue
		}
		w.add(r.id, issues)
		findings += len(issues)
	}
	if len(w.rowIDs) == 0 {
		return 0, nil
	}
	if err := s.inTx(ctx, func(q db.Querier) error { return w.persist(ctx, q) }); err != nil {
		return 0, err
	}
	return findings, nil
}

// loadOtherTargets reads, for the batch's distinct sources, the file's rows
// with that source.
func (s *Service) loadOtherTargets(ctx context.Context, sc segmentScope, rows []qaRow) (map[string][]db.ListTargetsForSourcesRow, error) {
	seen := make(map[string]bool, len(rows))
	var sources, hashes []string
	for _, r := range rows {
		if seen[r.source] {
			continue
		}
		seen[r.source] = true
		sum := md5.Sum([]byte(r.source))
		sources = append(sources, r.source)
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	found, err := s.queries.ListTargetsForSources(ctx, db.ListTargetsForSourcesParams{
		FileID: sc.fileID, TargetLanguage: sc.targetLanguage, Hashes: hashes, Sources: sources,
	})
	if err != nil {
		return nil, fmt.Errorf("list targets for sources: %w", err)
	}
	out := make(map[string][]db.ListTargetsForSourcesRow, len(sources))
	for _, f := range found {
		out[f.SourceText] = append(out[f.SourceText], f)
	}
	return out, nil
}

// issueWrite collects the findings of many rows for one set-based upsert and
// prune: persistIssues for each row, in two statements.
type issueWrite struct {
	rowIDs                        []int64
	upIDs                         []int64
	upChecks, upSeverities, upMsg []string
}

// add records a row's findings. A check that fires twice keeps its last
// issue, as consecutive single-row upserts would.
func (w *issueWrite) add(rowID int64, issues []Issue) {
	w.rowIDs = append(w.rowIDs, rowID)
	at := make(map[string]int, len(issues))
	for _, issue := range issues {
		if i, ok := at[issue.CheckType]; ok {
			w.upSeverities[i], w.upMsg[i] = issue.Severity, issue.Message
			continue
		}
		at[issue.CheckType] = len(w.upIDs)
		w.upIDs = append(w.upIDs, rowID)
		w.upChecks = append(w.upChecks, issue.CheckType)
		w.upSeverities = append(w.upSeverities, issue.Severity)
		w.upMsg = append(w.upMsg, issue.Message)
	}
}

func (w *issueWrite) persist(ctx context.Context, q db.Querier) error {
	if len(w.upIDs) > 0 {
		if err := q.UpsertQAResults(ctx, db.UpsertQAResultsParams{
			TranslationIds: w.upIDs, CheckTypes: w.upChecks, Severities: w.upSeverities, Messages: w.upMsg,
		}); err != nil {
			return fmt.Errorf("persist QA results: %w", err)
		}
	}
	if err := q.PruneQAResultsForTranslations(ctx, db.PruneQAResultsForTranslationsParams{
		TranslationIds: w.rowIDs, KeepIds: w.upIDs, KeepCheckTypes: w.upChecks,
	}); err != nil {
		return fmt.Errorf("prune QA results: %w", err)
	}
	return nil
}
