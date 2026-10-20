package mcp

// The project list and project detail, read through the app's own project
// list query, plus the workflow steps the project and job lists carry and the
// project's machine translation (backend_direct_projects_mt.go).

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/project"
	"openloc/internal/ptr"
)

func (b *directBackend) ListProjects(ctx context.Context, in ListProjectsIn) (ProjectList, error) {
	tenantID, _ := scope(ctx)
	if in.Cursor < 0 {
		return ProjectList{}, invalidf("cursor must be a next_cursor from a previous page")
	}
	page := httputil.CursorParams{Cursor: in.Cursor, Limit: int32(clampLimit(in.Limit))}
	// The app's project list query and filter checks: the customer comes from
	// the Customer label, and an unknown status is an error, not an empty list.
	params, err := project.ListParams(tenantID, page, project.ListFilters{
		Status: in.Status, CustomerID: in.CustomerID,
		Source: in.SourceLanguage, Target: in.TargetLanguage, Q: in.Query,
	})
	if err != nil {
		return ProjectList{}, invalidf("%s", err.Error())
	}
	rows, err := b.q.ListProjectsFiltered(ctx, params)
	if err != nil {
		return ProjectList{}, fmt.Errorf("list projects: %w", err)
	}
	var next int64
	if len(rows) > int(page.Limit) {
		rows = rows[:page.Limit]
		next = rows[len(rows)-1].ID
	}
	ids := make([]int64, len(rows))
	targets := make(map[int64][]string, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
		targets[p.ID] = p.TargetLanguages
	}
	steps, err := b.workflowStepNames(ctx, tenantID, ids)
	if err != nil {
		return ProjectList{}, err
	}
	mt, err := b.projectsMT(ctx, tenantID, targets)
	if err != nil {
		return ProjectList{}, err
	}
	out := make([]ProjectSummary, 0, len(rows))
	for _, p := range rows {
		row := ProjectSummary{
			ID:              p.ID,
			Name:            p.Name,
			Status:          string(p.Status),
			SourceLanguage:  p.SourceLanguage,
			TargetLanguages: p.TargetLanguages,
			Deadline:        tsString(p.Deadline),
			CreatedAt:       tsString(p.CreatedAt),
			CustomerID:      p.CustomerValueID,
			CustomerName:    p.CustomerName,
			WorkflowSteps:   steps[p.ID],
			StepCount:       len(steps[p.ID]),
		}
		mt[p.ID].apply(&row)
		out = append(out, row)
	}
	return ProjectList{Projects: out, Count: len(out), NextCursor: next}, nil
}

// workflowStepNames maps each project to its workflow step names in order;
// every id gets a non-nil slice.
func (b *directBackend) workflowStepNames(ctx context.Context, tenantID int64, projectIDs []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(projectIDs))
	for _, id := range projectIDs {
		out[id] = []string{}
	}
	if len(projectIDs) == 0 {
		return out, nil
	}
	rows, err := b.q.ListMCPProjectWorkflowSteps(ctx, db.ListMCPProjectWorkflowStepsParams{
		TenantID: tenantID, ProjectIds: projectIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow steps: %w", err)
	}
	for _, r := range rows {
		out[r.ProjectID] = append(out[r.ProjectID], r.Name)
	}
	return out, nil
}

// projectDetail is a project as get_project shows it: the customer from its
// Customer label pick, and its workflow steps.
func (b *directBackend) projectDetail(ctx context.Context, p db.Project) (ProjectDetail, error) {
	tenantID, _ := scope(ctx)
	summary := ProjectSummary{
		ID:              p.ID,
		Name:            p.Name,
		Status:          string(p.Status),
		SourceLanguage:  p.SourceLanguage,
		TargetLanguages: p.TargetLanguages,
		CreatedAt:       tsString(p.CreatedAt),
	}
	key := "customer"
	pick, err := b.q.GetProjectSystemLabelValue(ctx, db.GetProjectSystemLabelValueParams{
		ProjectID: p.ID, TenantID: tenantID, SystemKey: &key,
	})
	switch {
	case err == nil:
		summary.CustomerID, summary.CustomerName = &pick.ID, pick.Value
	case !errors.Is(err, pgx.ErrNoRows):
		return ProjectDetail{}, fmt.Errorf("get project customer: %w", err)
	}
	steps, err := b.workflowStepNames(ctx, tenantID, []int64{p.ID})
	if err != nil {
		return ProjectDetail{}, err
	}
	summary.WorkflowSteps, summary.StepCount = steps[p.ID], len(steps[p.ID])
	mt, err := b.projectsMT(ctx, tenantID, map[int64][]string{p.ID: p.TargetLanguages})
	if err != nil {
		return ProjectDetail{}, err
	}
	mt[p.ID].apply(&summary)
	return ProjectDetail{
		ProjectSummary: summary,
		Description:    p.Description,
		Deadline:       tsString(p.Deadline),
	}, nil
}

func (b *directBackend) GetProject(ctx context.Context, in GetProjectIn) (ProjectDetail, error) {
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return ProjectDetail{}, err
	}
	return b.projectDetail(ctx, p)
}

// jobSteps maps each job to all its workflow steps in order; every id gets a
// non-nil slice.
func (b *directBackend) jobSteps(ctx context.Context, tenantID int64, jobIDs []int64) (map[int64][]JobStepBrief, error) {
	out := make(map[int64][]JobStepBrief, len(jobIDs))
	for _, id := range jobIDs {
		out[id] = []JobStepBrief{}
	}
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := b.q.ListMCPJobSteps(ctx, db.ListMCPJobStepsParams{TenantID: tenantID, JobIds: jobIDs})
	if err != nil {
		return nil, fmt.Errorf("list job steps: %w", err)
	}
	for _, r := range rows {
		out[r.JobID] = append(out[r.JobID], JobStepBrief{
			Name: r.Name, Type: r.StepType, Status: r.Status,
			AssigneeID: r.AssignedTo, AssigneeName: ptr.Deref(r.AssigneeName),
			Deadline: tsString(r.Deadline),
		})
	}
	return out, nil
}
