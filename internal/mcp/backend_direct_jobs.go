package mcp

// The job list: one project's jobs or the whole organization's, soonest
// deadline first, a page at a time.

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

func (b *directBackend) ListJobs(ctx context.Context, in ListJobsIn) (JobList, error) {
	// project_id = 0 means org-wide; scoping is structural
	// via projects.tenant_id in the query, so no per-project check applies.
	// When a project IS named, the existence/authorisation check runs first so
	// a foreign project_id yields the same opaque not-found as everywhere
	// else, rather than an empty list that reads like "this project has no
	// jobs".
	if in.ProjectID != 0 {
		if _, err := b.project(ctx, in.ProjectID); err != nil {
			return JobList{}, err
		}
	}
	after, err := parseJobsCursor(in.Cursor)
	if err != nil {
		return JobList{}, err
	}
	tenantID, _ := scope(ctx)
	limit := clampLimit(in.Limit)
	rows, err := b.q.ListMCPJobs(ctx, db.ListMCPJobsParams{
		ProjectID:     in.ProjectID,
		TenantID:      tenantID,
		Status:        in.Status,
		OverdueOnly:   in.OverdueOnly,
		AssigneeID:    in.AssigneeID,
		AfterID:       after.id,
		AfterDeadline: after.afterDeadline(),
		RowLimit:      int32(limit + 1), // one extra row tells whether a next page exists
	})
	if err != nil {
		return JobList{}, fmt.Errorf("list jobs: %w", err)
	}
	var out JobList
	if len(rows) > limit {
		rows = rows[:limit]
		out.HasMore = true
		last := rows[len(rows)-1]
		out.NextCursor = jobsCursorAfter(last.JobID, last.StepDeadline).String()
	}
	ids := make([]int64, len(rows))
	for i, j := range rows {
		ids[i] = j.JobID
	}
	steps, err := b.jobSteps(ctx, tenantID, ids)
	if err != nil {
		return JobList{}, err
	}
	out.Jobs = make([]JobSummary, 0, len(rows))
	for _, j := range rows {
		out.Jobs = append(out.Jobs, JobSummary{
			ID:              j.JobID,
			FileID:          j.FileID,
			ProjectID:       j.ProjectID,
			ProjectName:     j.ProjectName,
			FileName:        j.FileName,
			TargetLanguage:  j.TargetLanguage,
			Status:          j.Status,
			CurrentStep:     j.CurrentStep,
			WordCount:       j.WordCount,
			SegmentCount:    j.SegmentCount,
			TranslatedCount: ptr.Deref(j.TranslatedCount),
			ErrorMessage:    ptr.Deref(j.ErrorMessage),
			MTSkipReason:    ptr.Deref(j.MtSkipReason),
			Deadline:        tsString(j.StepDeadline),
			Steps:           steps[j.JobID],
		})
	}
	out.Count = len(out.Jobs)
	return out, nil
}
