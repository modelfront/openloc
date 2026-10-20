package notification

import (
	"context"
	"errors"
	"time"

	db "openloc/internal/database/sqlcgen"
)

// JobFailure is one job that ended in status failed.
type JobFailure struct {
	TenantID  int64
	JobID     int64
	StartedBy *int64 // nil for an automated run
	MT        bool   // the machine translation step failed
	Reason    string // the job's error message
}

// JobFailureNotifier is what the failure paths depend on; *Service implements it.
type JobFailureNotifier interface {
	NotifyJobFailed(ctx context.Context, f JobFailure) error
}

// NotifyJobFailed tells the person who started the run and the project's
// creator, once each. It runs on a fresh context because the failure paths
// usually hold a cancelled one.
func (s *Service) NotifyJobFailed(ctx context.Context, f JobFailure) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	job, err := s.queries.GetJobTenant(ctx, db.GetJobTenantParams{ID: f.JobID, TenantID: f.TenantID})
	if err != nil {
		return err
	}
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: f.TenantID})
	if err != nil {
		return err
	}
	project, err := s.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: f.TenantID})
	if err != nil {
		return err
	}
	typ := EventJobFailed
	if f.MT {
		typ = EventMTFailed
	}
	e := Event{
		Type: typ, TenantID: f.TenantID, JobID: f.JobID, ProjectID: project.ID,
		ProjectName: project.Name, FileName: file.Name, TargetLang: job.TargetLanguage, Reason: f.Reason,
	}
	var errs []error
	for _, id := range failureRecipients(f.StartedBy, project.CreatedBy) {
		e.RecipientID = id
		errs = append(errs, s.Send(ctx, e))
	}
	return errors.Join(errs...)
}

// failureRecipients is the starter then the creator, without duplicates.
func failureRecipients(ids ...*int64) []int64 {
	var out []int64
	for _, id := range ids {
		if id == nil || *id == 0 {
			continue
		}
		dup := false
		for _, seen := range out {
			dup = dup || seen == *id
		}
		if !dup {
			out = append(out, *id)
		}
	}
	return out
}
