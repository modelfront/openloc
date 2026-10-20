// Package analysis implements saved leverage analyses. The worker reads stored
// `translations.tm_match_percent` + `translations.status` per job ×
// target_language, buckets into 6 bands with auto_approved subsets, and
// writes immutable `job_analysis` rows.
//
// `tm_101` (101% in-context exact) is populated from
// translations.tm_match_percent=101. Repetitions are computed
// project-wide across the analysis' whole job set: the 2nd+ occurrence of an
// identical (target_language, source_text) is subtracted from its leverage band
// and counted in `repetitions` instead.
package analysis

import (
	"context"
	"errors"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/pipeline"
)

// FailedMessage is the error_message stored on a failed analysis. The UI shows
// it verbatim, so the internal error goes to the log, never into the row.
const FailedMessage = "The analysis could not be completed. Run a new analysis to try again."

// errClaimedElsewhere ends a run whose analysis another worker holds or has
// already finished. It never leaves Run.
var errClaimedElsewhere = errors.New("analysis claimed by another worker")

// Service drives the analysis worker. Stateless; one instance per app.
type Service struct {
	queries db.Querier
	txr     pipeline.TxRunner
}

func NewService(q db.Querier, txr pipeline.TxRunner) *Service {
	return &Service{queries: q, txr: txr}
}

// Run executes the analysis identified by analysisID, end to end. The
// caller (asynq handler) passes tenantID from the task payload — the
// handler validated tenant ownership on POST; carrying it in the payload
// lets the worker reuse the tenant-scoped GetAnalysis read.
//
// Flow (see planning doc §"Trigger & flow"):
//  1. Load the analysis (tenant-scoped). Terminal rows no-op.
//  2. In one transaction: claim the row (pending → running), aggregate all
//     jobs' bands (incl. project-wide repetitions) in one
//     GetSavedAnalysisRepetitionBands call, write one InsertJobAnalysis row
//     per job, and mark it completed. Cross-tenant job_ids fail the run.
//  3. On any error the transaction rolls back (no partial breakdown) and the
//     row is marked failed with FailedMessage.
//
// More than one task can carry the same analysis (every upload and every
// finished pretranslate job may promote it). The claim holds the row lock
// until commit, so a second worker's claim waits, then finds the row no
// longer pending and returns nil without writing anything. A worker that
// dies mid-run rolls back to pending, so the asynq retry runs it again.
func (s *Service) Run(ctx context.Context, analysisID, tenantID int64) error {
	a, err := s.queries.GetAnalysis(ctx, db.GetAnalysisParams{
		ID:       analysisID,
		TenantID: tenantID,
	})
	if err != nil {
		return fmt.Errorf("load analysis %d (tenant %d): %w", analysisID, tenantID, err)
	}

	// Terminal states: no-op cleanly (asynq retry of a completed run).
	if a.Status == db.AnalysisStatusCompleted || a.Status == db.AnalysisStatusFailed {
		logging.Info(ctx, "analysis already finished, skipping", "analysis.skip_terminal",
			logging.AnalysisID(analysisID), logging.Status(string(a.Status)))
		return nil
	}

	var jobCount int
	err = s.txr.InTx(ctx, func(q db.Querier) error {
		n, err := s.compute(ctx, q, a)
		jobCount = n
		return err
	})
	if errors.Is(err, errClaimedElsewhere) {
		logging.Info(ctx, "analysis claimed by another worker, skipping", "analysis.skip_claimed",
			logging.AnalysisID(analysisID))
		return nil
	}
	if err != nil {
		return s.fail(ctx, analysisID, err)
	}
	logging.Info(ctx, "analysis completed", "analysis.completed",
		logging.AnalysisID(analysisID), logging.Count(jobCount))
	return nil
}

// compute does the whole run on the transaction-bound q and returns the number
// of jobs analyzed. errClaimedElsewhere means the row was not pending.
func (s *Service) compute(ctx context.Context, q db.Querier, a db.Analysis) (int, error) {
	claimed, err := q.UpdateAnalysisRunning(ctx, a.ID)
	if err != nil {
		return 0, fmt.Errorf("transition to running: %w", err)
	}
	if claimed == 0 {
		return 0, errClaimedElsewhere
	}

	// Resolve the job set. Manual analyses carry an explicit, validated
	// job_ids array. The coalesced auto analysis instead resolves
	// the project's current jobs at run time, so files that joined the upload
	// batch after the pending row was created are still included.
	jobIDs := a.JobIds
	if a.IsAuto {
		projJobs, err := q.ListProjectJobsForAutoAnalysis(ctx, a.ProjectID)
		if err != nil {
			return 0, fmt.Errorf("resolve project jobs: %w", err)
		}
		jobIDs = make([]int64, len(projJobs))
		for i, j := range projJobs {
			jobIDs[i] = j.ID
		}
		// Persist the resolved set back onto the row so the stored job_ids (and
		// the UI's "jobs" count) reflect what was actually analyzed — otherwise
		// an auto row shows 0 jobs despite a populated breakdown.
		if err := q.SetAnalysisJobIDs(ctx, db.SetAnalysisJobIDsParams{ID: a.ID, JobIds: jobIDs}); err != nil {
			return 0, fmt.Errorf("persist resolved job_ids: %w", err)
		}
	}

	// Fetch band aggregates for the whole job set in one round-trip. Repetitions
	// are project-wide, so the window must span every job in the set — a per-job
	// query can't see cross-file repeats. Group the flat rows back by job so each
	// job's job_analysis row is written from its own slice.
	bandRows, err := q.GetSavedAnalysisRepetitionBands(ctx, jobIDs)
	if err != nil {
		return 0, fmt.Errorf("aggregate bands: %w", err)
	}
	rowsByJob := make(map[int64][]db.GetSavedAnalysisRepetitionBandsRow, len(jobIDs))
	for _, b := range bandRows {
		rowsByJob[b.JobID] = append(rowsByJob[b.JobID], b)
	}

	for _, jobID := range jobIDs {
		if err := computeOneJob(ctx, q, a, jobID, rowsByJob[jobID]); err != nil {
			return 0, fmt.Errorf("job %d: %w", jobID, err)
		}
	}

	if err := q.UpdateAnalysisCompleted(ctx, a.ID); err != nil {
		return 0, fmt.Errorf("transition to completed: %w", err)
	}
	return len(jobIDs), nil
}

// computeOneJob pivots the pre-fetched band aggregates for one job into the
// wide row shape and writes one job_analysis row. bandRows are this job's slice
// of the batch GetSavedAnalysisRepetitionBands result (may be empty for a job
// with no segments — a zero row is still written).
// Tenant isolation: we re-read job context via GetJobForAnalysis and
// compare its tenant_id to the analysis's; mismatch is a poison condition.
func computeOneJob(ctx context.Context, q db.Querier, a db.Analysis, jobID int64, bandRows []db.GetSavedAnalysisRepetitionBandsRow) error {
	ctxJob, err := q.GetJobForAnalysis(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job context: %w", err)
	}
	if ctxJob.TenantID != a.TenantID {
		return fmt.Errorf("tenant mismatch: analysis=%d job=%d (tenant %d vs %d)", a.ID, jobID, a.TenantID, ctxJob.TenantID)
	}

	// Pivot into the wide row. Bands not represented in bandRows stay zero.
	row := db.InsertJobAnalysisParams{
		AnalysisID:      a.ID,
		JobID:           &jobID,
		TargetLanguage:  ctxJob.TargetLanguage,
		JobNameSnapshot: ctxJob.FileName,
	}
	for _, b := range bandRows {
		applyBand(&row, b)
	}

	if err := q.InsertJobAnalysis(ctx, row); err != nil {
		return fmt.Errorf("insert breakdown: %w", err)
	}
	return nil
}

// applyBand assigns a (band, is_auto_approved, is_repetition) aggregate into the
// matching columns of the wide row. The auto_approved metrics are a strict
// subset: when is_auto_approved=true the same numbers go into the totals AND the
// _auto_approved columns (a single source segment counted once in totals, once
// in subset). The schema CHECK constraint pins this invariant.
//
// Repetitions are mutually exclusive with the leverage bands: a
// repetition row (2nd+ identical source across the job set) is routed ONLY to
// the repetitions columns, never also to tm_101/…/new — this "subtraction"
// keeps the per-job band counts summing to the segment total.
//
// `weighted_words` stays zero — weights are not applied to saved analyses.
func applyBand(row *db.InsertJobAnalysisParams, b db.GetSavedAnalysisRepetitionBandsRow) {
	if b.IsRepetition {
		row.RepetitionsSegments += b.Segments
		row.RepetitionsWords += b.Words
		row.RepetitionsCharacters += b.Characters
		if b.IsAutoApproved {
			row.RepetitionsSegmentsAutoApproved += b.Segments
			row.RepetitionsWordsAutoApproved += b.Words
			row.RepetitionsCharactersAutoApproved += b.Characters
		}
		return
	}
	// Switch on band name; assign segments/words/characters into both the
	// totals and (when is_auto_approved) the subset columns. Verbose but
	// explicit — easier to audit than reflection.
	switch b.Band {
	case "tm_101":
		row.Tm101Segments += b.Segments
		row.Tm101Words += b.Words
		row.Tm101Characters += b.Characters
		if b.IsAutoApproved {
			row.Tm101SegmentsAutoApproved += b.Segments
			row.Tm101WordsAutoApproved += b.Words
			row.Tm101CharactersAutoApproved += b.Characters
		}
	case "tm_100":
		row.Tm100Segments += b.Segments
		row.Tm100Words += b.Words
		row.Tm100Characters += b.Characters
		if b.IsAutoApproved {
			row.Tm100SegmentsAutoApproved += b.Segments
			row.Tm100WordsAutoApproved += b.Words
			row.Tm100CharactersAutoApproved += b.Characters
		}
	case "tm_85_99":
		row.Tm8599Segments += b.Segments
		row.Tm8599Words += b.Words
		row.Tm8599Characters += b.Characters
		if b.IsAutoApproved {
			row.Tm8599SegmentsAutoApproved += b.Segments
			row.Tm8599WordsAutoApproved += b.Words
			row.Tm8599CharactersAutoApproved += b.Characters
		}
	case "tm_65_84":
		row.Tm6584Segments += b.Segments
		row.Tm6584Words += b.Words
		row.Tm6584Characters += b.Characters
		if b.IsAutoApproved {
			row.Tm6584SegmentsAutoApproved += b.Segments
			row.Tm6584WordsAutoApproved += b.Words
			row.Tm6584CharactersAutoApproved += b.Characters
		}
	case "new":
		row.NewSegments += b.Segments
		row.NewWords += b.Words
		row.NewCharacters += b.Characters
		if b.IsAutoApproved {
			row.NewSegmentsAutoApproved += b.Segments
			row.NewWordsAutoApproved += b.Words
			row.NewCharactersAutoApproved += b.Characters
		}
	}
}

// markFailed marks the analysis failed with FailedMessage. Every failure path
// uses it: error_message is shown to managers in the UI, so the internal error
// goes to the log only.
func markFailed(ctx context.Context, q db.Querier, id int64) error {
	msg := FailedMessage
	return q.UpdateAnalysisFailed(ctx, db.UpdateAnalysisFailedParams{ID: id, ErrorMessage: &msg})
}

// fail marks the analysis failed and returns runErr so the asynq layer
// records it.
func (s *Service) fail(ctx context.Context, id int64, runErr error) error {
	if err := markFailed(ctx, s.queries, id); err != nil {
		logging.Error(ctx, errors.Join(runErr, err), "analysis failed and marking it failed also failed", "analysis.fail_status_write",
			logging.AnalysisID(id))
		return runErr
	}
	logging.Error(ctx, runErr, "analysis failed", "analysis.failed", logging.AnalysisID(id))
	return runErr
}
