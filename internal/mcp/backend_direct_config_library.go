package mcp

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/stepdef"
	"openloc/internal/workflowlib"
)

// Step definitions and library workflows. A project or template copies a
// workflow's steps when it is set up, so editing or deleting a library entry
// never changes a running project; previews say how far the provenance reaches.

func (b *directBackend) GetStepDefinition(ctx context.Context, in GetStepDefinitionIn) (StepDefinitionDetail, error) {
	tenantID, _ := scope(ctx)
	sd, err := b.q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: in.StepDefinitionID, TenantID: tenantID})
	if err != nil {
		return StepDefinitionDetail{}, configErr(err)
	}
	return b.stepDefinitionDetail(ctx, tenantID, sd)
}

func (b *directBackend) stepDefinitionDetail(ctx context.Context, tenantID int64, sd db.StepDefinition) (StepDefinitionDetail, error) {
	using, err := b.q.ListWorkflowsUsingStepDefinition(ctx, db.ListWorkflowsUsingStepDefinitionParams{StepDefinitionID: sd.ID, TenantID: tenantID})
	if err != nil {
		return StepDefinitionDetail{}, fmt.Errorf("list workflows using step definition: %w", err)
	}
	out := StepDefinitionDetail{
		StepDefinitionID: sd.ID, Name: sd.Name, StepType: sd.StepType,
		AIRouterID: ptr.Deref(sd.AiRouterID), WorkflowsUsing: make([]string, 0, len(using)),
	}
	for _, u := range using {
		out.WorkflowsUsing = append(out.WorkflowsUsing, u.Name)
	}
	return out, nil
}

func (b *directBackend) GetWorkflow(ctx context.Context, in GetWorkflowIn) (WorkflowDetail, error) {
	tenantID, _ := scope(ctx)
	wf, err := b.q.GetWorkflow(ctx, db.GetWorkflowParams{ID: in.WorkflowID, TenantID: tenantID})
	if err != nil {
		return WorkflowDetail{}, configErr(err)
	}
	return b.workflowDetail(ctx, tenantID, wf)
}

func (b *directBackend) workflowDetail(ctx context.Context, tenantID int64, wf db.Workflow) (WorkflowDetail, error) {
	d, err := workflowlib.LoadDetail(ctx, b.q, tenantID, wf)
	if err != nil {
		return WorkflowDetail{}, err
	}
	copies, err := b.workflowCopies(ctx, tenantID, wf.ID)
	if err != nil {
		return WorkflowDetail{}, err
	}
	return workflowDetailOf(d.Workflow, d.Steps, copies), nil
}

func (b *directBackend) workflowCopies(ctx context.Context, tenantID, workflowID int64) (db.CountProfilesFromWorkflowRow, error) {
	copies, err := b.q.CountProfilesFromWorkflow(ctx, db.CountProfilesFromWorkflowParams{WorkflowID: &workflowID, TenantID: tenantID})
	if err != nil {
		return db.CountProfilesFromWorkflowRow{}, fmt.Errorf("count workflow copies: %w", err)
	}
	return copies, nil
}

func workflowDetailOf(wf db.Workflow, steps []db.ListWorkflowDefinitionStepsRow, copies db.CountProfilesFromWorkflowRow) WorkflowDetail {
	out := WorkflowDetail{
		WorkflowID: wf.ID, Name: wf.Name, Steps: make([]WorkflowStepDetail, 0, len(steps)),
		CopiedByProjects: copies.Projects, CopiedByTemplates: copies.Templates,
	}
	for _, s := range steps {
		out.Steps = append(out.Steps, WorkflowStepDetail{
			StepDefinitionID: s.StepDefinitionID, Name: s.Name, StepType: s.StepType,
			DefaultAssigneeID: ptr.Deref(s.DefaultAssignee), AssigneeName: ptr.Deref(s.AssigneeName),
			DefinitionDeleted: s.DefinitionDeleted,
		})
	}
	return out
}

func configStepDefInput(in CreateStepDefinitionIn) stepdef.Input {
	out := stepdef.Input{Name: in.Name, StepType: strings.TrimSpace(strings.ToLower(in.StepType))}
	if in.AIRouterID != 0 {
		id := in.AIRouterID
		out.AIRouterID = &id
	}
	return out
}

func (b *directBackend) PreviewUpdateStepDefinition(ctx context.Context, in UpdateStepDefinitionIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	sdIn := configStepDefInput(in.CreateStepDefinitionIn)
	sd, err := stepdef.ValidateUpdate(ctx, b.q, tenantID, in.StepDefinitionID, &sdIn)
	if err != nil {
		return Preview{}, configErr(err)
	}
	detail, err := b.stepDefinitionDetail(ctx, tenantID, sd)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Summary: fmt.Sprintf("Update the step %q in the step library.", sd.Name)}
	if n := len(detail.WorkflowsUsing); n > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Composed into %s: %s. Projects and templates that already copied it keep their copy.",
			configPlural(int64(n), "library workflow"), strings.Join(detail.WorkflowsUsing, ", ")))
	}
	return p, nil
}

func (b *directBackend) UpdateStepDefinition(ctx context.Context, in UpdateStepDefinitionIn) (StepDefinitionDetail, error) {
	tenantID, _ := scope(ctx)
	sd, err := stepdef.Update(ctx, b.q, tenantID, in.StepDefinitionID, configStepDefInput(in.CreateStepDefinitionIn))
	if err != nil {
		return StepDefinitionDetail{}, configErr(err)
	}
	return b.stepDefinitionDetail(ctx, tenantID, sd)
}

func (b *directBackend) PreviewDeleteStepDefinition(ctx context.Context, in DeleteStepDefinitionIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	sd, err := stepdef.CheckDelete(ctx, b.q, tenantID, in.StepDefinitionID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{
		Summary: fmt.Sprintf("Remove the step %q from the step library.", sd.Name),
		Effects: []string{"No library workflow uses it. Projects and templates that copied it keep their step."},
	}, nil
}

func (b *directBackend) DeleteStepDefinition(ctx context.Context, in DeleteStepDefinitionIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("deleting a step"); err != nil {
		return DeletedConfig{}, err
	}
	sd, err := b.q.GetStepDefinition(ctx, db.GetStepDefinitionParams{ID: in.StepDefinitionID, TenantID: tenantID})
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := stepdef.Delete(ctx, b.pool, tenantID, sd.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: sd.ID, Name: sd.Name}, nil
}

func configWorkflowInput(in UpdateWorkflowIn) workflowlib.Input {
	out := workflowlib.Input{Name: in.Name, Steps: make([]workflowlib.StepInput, 0, len(in.Steps))}
	for _, s := range in.Steps {
		step := workflowlib.StepInput{StepDefinitionID: s.StepDefinitionID}
		if s.DefaultAssigneeID != 0 {
			id := s.DefaultAssigneeID
			step.DefaultAssignee = &id
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

// configWorkflowCopies phrases how many projects and templates copied a workflow.
func configWorkflowCopies(d WorkflowDetail) []string {
	if d.CopiedByProjects == 0 && d.CopiedByTemplates == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s and %s copied their steps from it; they keep their own copy.",
		configPlural(d.CopiedByProjects, "project"), configPlural(d.CopiedByTemplates, "template"))}
}

func (b *directBackend) PreviewUpdateWorkflow(ctx context.Context, in UpdateWorkflowIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	wfIn := configWorkflowInput(in)
	wf, err := workflowlib.ValidateUpdate(ctx, b.q, tenantID, in.WorkflowID, &wfIn)
	if err != nil {
		return Preview{}, configErr(err)
	}
	detail, err := b.workflowDetail(ctx, tenantID, wf)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Update the workflow %q: %s.", wf.Name, configPlural(int64(len(wfIn.Steps)), "step")),
		Effects: configWorkflowCopies(detail),
	}, nil
}

func (b *directBackend) UpdateWorkflow(ctx context.Context, in UpdateWorkflowIn) (WorkflowDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating a workflow"); err != nil {
		return WorkflowDetail{}, err
	}
	// An update leaves the copy count as it is, so it is read first and
	// nothing is read after the commit.
	copies, err := b.workflowCopies(ctx, tenantID, in.WorkflowID)
	if err != nil {
		return WorkflowDetail{}, err
	}
	d, err := workflowlib.Update(ctx, b.q, b.pool, tenantID, in.WorkflowID, configWorkflowInput(in))
	if err != nil {
		return WorkflowDetail{}, configErr(err)
	}
	return workflowDetailOf(d.Workflow, d.Steps, copies), nil
}

func (b *directBackend) PreviewDeleteWorkflow(ctx context.Context, in DeleteWorkflowIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	wf, err := b.q.GetWorkflow(ctx, db.GetWorkflowParams{ID: in.WorkflowID, TenantID: tenantID})
	if err != nil {
		return Preview{}, configErr(err)
	}
	detail, err := b.workflowDetail(ctx, tenantID, wf)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Summary: fmt.Sprintf("Delete the workflow %q from the library.", wf.Name), Effects: configWorkflowCopies(detail)}, nil
}

func (b *directBackend) DeleteWorkflow(ctx context.Context, in DeleteWorkflowIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	wf, err := b.q.GetWorkflow(ctx, db.GetWorkflowParams{ID: in.WorkflowID, TenantID: tenantID})
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := workflowlib.Delete(ctx, b.q, tenantID, wf.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: wf.ID, Name: wf.Name}, nil
}
