package qa

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// CheckRow is one row CheckAndPersist checks: its proposed target and its
// source from the job's own segment.
type CheckRow struct {
	ID                 int64
	Source, SourceRich string
	Target, TargetRich string
}

// CheckAndPersist is Check followed by Persist for many rows of the job: the
// glossary is loaded once per checker, the other targets of the rows' sources
// and their stored ignores once per call, and every row's findings are
// replaced in one transaction. The results are keyed by row id.
func (c *JobChecker) CheckAndPersist(ctx context.Context, rows []CheckRow) (map[int64]CheckResult, error) {
	out := make(map[int64]CheckResult, len(rows))
	if !c.js.profileAssigned {
		for _, r := range rows {
			out[r.ID] = CheckResult{ProfileAssigned: false}
		}
		return out, nil
	}
	if len(rows) == 0 {
		return out, nil
	}

	lk := batchLookups{}
	if !c.js.rs.empty() {
		if !c.termsLoaded {
			terms, err := c.s.loadRunTerms(ctx, c.js)
			if err != nil {
				return nil, err
			}
			c.terms, c.termsLoaded = terms, true
		}
		lk.terms = c.terms
		if c.js.rs.enabled("consistency") {
			qr := make([]qaRow, len(rows))
			for i, r := range rows {
				qr[i] = qaRow{id: r.ID, source: r.Source}
			}
			others, err := c.s.loadOtherTargets(ctx, c.js.sc, qr)
			if err != nil {
				return nil, err
			}
			lk.others = others
		}
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	ignoredRows, err := c.s.queries.ListIgnoredQAResultsForTranslations(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list stored QA results: %w", err)
	}
	ignored := make(map[int64]map[string]string)
	for _, ig := range ignoredRows {
		if ignored[ig.TranslationID] == nil {
			ignored[ig.TranslationID] = map[string]string{}
		}
		ignored[ig.TranslationID][ig.CheckType] = ig.Message
	}

	var w issueWrite
	for _, r := range rows {
		var issues []Issue
		if !c.js.rs.empty() {
			issues = c.s.checkSegment(ctx, c.js.rs, lk, r.ID, r.Source, r.Target, r.SourceRich, r.TargetRich)
		}
		w.add(r.ID, issues)
		out[r.ID] = CheckResult{Issues: applyIgnored(issues, ignored[r.ID]), ProfileAssigned: true}
	}
	if err := c.s.inTx(ctx, func(q db.Querier) error { return w.persist(ctx, q) }); err != nil {
		return nil, err
	}
	return out, nil
}
