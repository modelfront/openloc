package qa

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/editor"
	"openloc/internal/logging"
	"openloc/internal/segmentbulk"
)

// RunAndPersistScope runs QA on the jobs of an editor scope, which share one
// project and one target language: every row of each job, or, with ids, each
// job's rows among them (a job that holds none is not opened). The project's
// settings, rules and terms load once. A job that fails is reported and the
// others still run; a cancelled request stops the run. The scope must come
// from editor.RequestScope.
func (s *Service) RunAndPersistScope(ctx context.Context, scope editor.Scope, ids []int64) ([]ScopeRunJob, error) {
	out := make([]ScopeRunJob, len(scope.Jobs))
	for i, j := range scope.Jobs {
		out[i] = ScopeRunJob{JobID: j.JobID, Outcome: ScopeRunDone}
	}
	first, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: scope.Jobs[0].JobID, TenantID: scope.TenantID})
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	base, err := s.resolveJobScope(ctx, first, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if !base.profileAssigned {
		for i := range out {
			out[i].Outcome = ScopeRunNoProfile
		}
		return out, nil
	}
	var byFile map[int64][]int64
	if ids != nil {
		if byFile, err = s.idsByFile(ctx, scope, ids); err != nil {
			return nil, err
		}
	}
	// The terms are the project's, the same for every job: loaded on first use.
	var terms *termIndex
	termsLoaded := false
	loadTerms := func() (*termIndex, error) {
		if !termsLoaded {
			idx, err := s.loadRunTerms(ctx, base)
			if err != nil {
				return nil, err
			}
			terms, termsLoaded = idx, true
		}
		return terms, nil
	}

	for i, j := range scope.Jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		own := byFile[j.FileID]
		if ids != nil && len(own) == 0 {
			continue
		}
		res, err := s.runScopeJob(ctx, j.JobID, scope.TenantID, base, own, ids != nil, loadTerms)
		out[i].Issues = res.Findings
		if err != nil {
			logging.Error(ctx, err, "QA run failed", "qa.run_scope_job", logging.JobID(j.JobID))
			out[i].Outcome = ScopeRunFailed
		}
	}
	return out, nil
}

// runScopeJob runs one job of a scope run with the scope's rules: all its
// rows, or its rows among ids when selection is set.
func (s *Service) runScopeJob(ctx context.Context, jobID, tenantID int64, base jobScope,
	ids []int64, selection bool, terms func() (*termIndex, error)) (JobRunResult, error) {
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return JobRunResult{}, fmt.Errorf("get job: %w", err)
	}
	js := base
	js.sc.fileID = job.FileID
	if selection {
		return s.runSelection(ctx, job, js, ids, terms)
	}
	// No enabled rules: the job keeps no findings, as in a job run.
	if js.rs.empty() {
		if err := s.queries.DeleteQAResultsForJob(ctx, db.DeleteQAResultsForJobParams{
			FileID: job.FileID, TargetLanguage: job.TargetLanguage,
		}); err != nil {
			return JobRunResult{}, fmt.Errorf("clear QA results: %w", err)
		}
		return JobRunResult{NoRules: true}, nil
	}
	idx, err := terms()
	if err != nil {
		return JobRunResult{}, err
	}
	return s.runJobPages(ctx, job, js, idx)
}

// idsByFile sorts the selected translations by the scope file they are in,
// in the order given, leaving out ids that are not translations of the scope.
func (s *Service) idsByFile(ctx context.Context, scope editor.Scope, ids []int64) (map[int64][]int64, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTranslationFilesForQA(ctx, db.ListTranslationFilesForQAParams{
		Ids: ids, FileIds: scope.FileIDs(), TargetLanguage: scope.TargetLanguage, TenantID: scope.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve translation files: %w", err)
	}
	fileOf := make(map[int64]int64, len(rows))
	for _, r := range rows {
		fileOf[r.TranslationID] = r.FileID
	}
	out := make(map[int64][]int64)
	for _, id := range ids {
		if f, ok := fileOf[id]; ok {
			out[f] = append(out[f], id)
		}
	}
	return out, nil
}
