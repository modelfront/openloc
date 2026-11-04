package qa

import (
	"context"
	"fmt"
	"slices"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/segmentbulk"
)

// jobScope is what every check on one job's rows shares: the segment scope
// and the enabled rules. profileAssigned is false when the project has no QA
// profile (rs is then empty).
type jobScope struct {
	sc              segmentScope
	rs              ruleSet
	profileAssigned bool
}

// resolveJobScope loads the job's project, QA profile and rules. The job must
// come from a tenant-scoped lookup.
func (s *Service) resolveJobScope(ctx context.Context, job db.Job, tenantID int64) (jobScope, error) {
	project, profile, err := s.jobSettings(ctx, job, tenantID)
	if err != nil {
		return jobScope{}, err
	}
	js := jobScope{sc: segmentScope{
		fileID:            job.FileID,
		settingsProfileID: project.SettingsProfileID,
		sourceLanguage:    project.SourceLanguage,
		targetLanguage:    job.TargetLanguage,
		projectDomain:     ptr.Deref(project.Domain),
	}}
	if profile.QaProfileID == nil {
		return js, nil
	}
	if js.rs, err = s.loadRules(ctx, *profile.QaProfileID, job.TargetLanguage); err != nil {
		return jobScope{}, err
	}
	js.profileAssigned = true
	return js, nil
}

// JobHasQAProfile reports whether the job's project has a QA profile, which a
// QA run needs. The job must come from a tenant-scoped lookup.
func (s *Service) JobHasQAProfile(ctx context.Context, job db.Job, tenantID int64) (bool, error) {
	_, profile, err := s.jobSettings(ctx, job, tenantID)
	if err != nil {
		return false, err
	}
	return profile.QaProfileID != nil, nil
}

// jobSettings loads the job's project and its settings profile. The job must
// come from a tenant-scoped lookup.
func (s *Service) jobSettings(ctx context.Context, job db.Job, tenantID int64) (db.Project, db.SettingsProfile, error) {
	// safe: job.FileID is FK-bound to the tenant-scoped job row.
	file, err := s.queries.GetFile(ctx, job.FileID)
	if err != nil {
		return db.Project{}, db.SettingsProfile{}, fmt.Errorf("get file: %w", err)
	}
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
	if err != nil {
		return db.Project{}, db.SettingsProfile{}, fmt.Errorf("get project: %w", err)
	}
	// safe: project.SettingsProfileID is FK-bound through the validated chain.
	profile, err := s.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return db.Project{}, db.SettingsProfile{}, fmt.Errorf("get settings profile: %w", err)
	}
	return project, profile, nil
}

// JobChecker runs the confirm-time QA check on many rows of one job, loading
// the project, its rules and its glossary once instead of once per row.
type JobChecker struct {
	s  *Service
	js jobScope
	// terms is the glossary, loaded by the first CheckAndPersist.
	terms       *termIndex
	termsLoaded bool
}

// JobChecker builds a checker for the job's rows. The job must come from a
// tenant-scoped lookup.
func (s *Service) JobChecker(ctx context.Context, job db.Job, tenantID int64) (*JobChecker, error) {
	js, err := s.resolveJobScope(ctx, job, tenantID)
	if err != nil {
		return nil, err
	}
	return &JobChecker{s: s, js: js}, nil
}

// RunAndPersistSelection is RunAndPersistJob for the given rows of the job:
// it checks their stored text and replaces their findings. Ids that are not
// rows of the job are left alone. Returns ErrNoQAProfile when the project has
// no QA profile.
func (s *Service) RunAndPersistSelection(ctx context.Context, jobID, tenantID int64, ids []int64) (JobRunResult, error) {
	ids, err := segmentbulk.NormalizeIDs(ids)
	if err != nil {
		return JobRunResult{}, err
	}
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: jobID, TenantID: tenantID})
	if err != nil {
		return JobRunResult{}, fmt.Errorf("get job: %w", err)
	}
	js, err := s.resolveJobScope(ctx, job, tenantID)
	if err != nil {
		return JobRunResult{}, err
	}
	if !js.profileAssigned {
		return JobRunResult{}, ErrNoQAProfile
	}
	return s.runSelection(ctx, job, js, ids, func() (*termIndex, error) { return s.loadRunTerms(ctx, js) })
}

// runSelection checks the job's rows among ids. terms loads the run's terms;
// it is not called when no id is a row of the job.
func (s *Service) runSelection(ctx context.Context, job db.Job, js jobScope, ids []int64,
	terms func() (*termIndex, error)) (JobRunResult, error) {
	rows, err := segmentbulk.JobRows(ctx, s.queries, job, auth.UserIDFromContext(ctx), ids)
	if err != nil {
		return JobRunResult{}, err
	}
	found := make([]int64, 0, len(rows))
	for _, id := range ids {
		if _, ok := rows[id]; ok {
			found = append(found, id)
		}
	}
	// No enabled rules: the selected rows keep no findings, as in a job run.
	if js.rs.empty() {
		if err := s.queries.DeleteQAResultsForTranslations(ctx, found); err != nil {
			return JobRunResult{}, fmt.Errorf("clear QA results: %w", err)
		}
		return JobRunResult{NoRules: true}, nil
	}

	if len(found) == 0 {
		return JobRunResult{}, nil
	}
	idx, err := terms()
	if err != nil {
		return JobRunResult{}, err
	}
	var result JobRunResult
	for page := range slices.Chunk(found, runPageSize) {
		batch := make([]qaRow, 0, len(page))
		for _, id := range page {
			r := rows[id]
			batch = append(batch, qaRow{id: r.ID, source: r.SourceText, target: ptr.Deref(r.ActiveText),
				sourceRich: ptr.Deref(r.SourceRichText), targetRich: ptr.Deref(r.ActiveRichText)})
		}
		n, err := s.runRows(ctx, js, idx, batch)
		result.Findings += n
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
