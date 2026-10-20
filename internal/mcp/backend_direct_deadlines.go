package mcp

// Workflow-step deadlines over MCP. A project's jobs are file × target
// language, so a step can be due on a different date per language; the pair
// here reads the schedule and patches it.
//
// Both tools go through the same service the REST handler calls
// (project.ReplaceStepsWithAudit → settings.ReplaceWorkflowStepsTx), so the
// two surfaces validate, propagate to in-flight jobs, and audit identically.

import (
	"context"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/langcode"
	"openloc/internal/project"
	"openloc/internal/settings"
	"openloc/internal/stepdef"
)

func (b *directBackend) ListProjectWorkflowSteps(ctx context.Context, in ListProjectWorkflowStepsIn) (ProjectWorkflowStepList, error) {
	tenantID, _ := scope(ctx)
	// Tenant-scoped read first; everything below works from ids it returned.
	// Through the helper, so another tenant's id answers "not found" rather
	// than an internal error.
	proj, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return ProjectWorkflowStepList{}, err
	}
	items, err := b.workflowStepSummaries(ctx, tenantID, proj.SettingsProfileID)
	if err != nil {
		return ProjectWorkflowStepList{}, err
	}
	return ProjectWorkflowStepList{
		ProjectID: proj.ID, TargetLanguages: proj.TargetLanguages,
		Items: items, Count: len(items),
	}, nil
}

func (b *directBackend) SetWorkflowStepDeadlines(ctx context.Context, in SetWorkflowStepDeadlinesIn) (SetWorkflowStepDeadlinesResult, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("setting deadlines"); err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}
	if len(in.Deadlines) == 0 {
		return SetWorkflowStepDeadlinesResult{}, invalidf("deadlines is required — give at least one workflow_step_id and date")
	}
	proj, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}

	existing, err := b.q.ListSettingsWorkflowSteps(ctx, proj.SettingsProfileID)
	if err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}
	current, err := settings.LoadStepDeadlines(ctx, b.q, proj.SettingsProfileID)
	if err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}
	steps, err := patchStepDeadlines(existing, current, in.Deadlines)
	if err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}

	// A deadline patch never changes what a step IS, so no step can be
	// appended and there is no AI step for the caller to kick off — the one
	// thing the REST handler does on top of this.
	if _, err := project.ReplaceStepsWithAudit(ctx, b.pool, b.q, tenantID, proj, steps); err != nil {
		// A rejected date or language is the model's to fix, so it has to reach
		// it verbatim rather than as the generic "logged" internal error.
		if msg, ok := httputil.AsClientError(err); ok {
			return SetWorkflowStepDeadlinesResult{}, invalidf("%s", msg)
		}
		return SetWorkflowStepDeadlinesResult{}, err
	}

	items, err := b.workflowStepSummaries(ctx, tenantID, proj.SettingsProfileID)
	if err != nil {
		return SetWorkflowStepDeadlinesResult{}, err
	}
	return SetWorkflowStepDeadlinesResult{
		ProjectID: proj.ID, Items: items, Count: len(items),
		Note: "Jobs already on these steps were re-dated too, except ones already completed. " +
			"A language with no date of its own uses the step's.",
	}, nil
}

// patchStepDeadlines turns the profile's current steps into the full step list
// ReplaceWorkflowSteps wants, with the requested dates applied on top. Identity
// is carried forward untouched: a library-backed row by its definition, a
// pre-library row by its own id.
//
// A patch, not a replace — a step the caller left out keeps the dates it had,
// so a tool call about one language cannot silently clear the others.
func patchStepDeadlines(existing []db.WorkflowStep, current settings.StepDeadlines, patch []WorkflowStepDeadlineIn) ([]settings.StepInput, error) {
	byID := make(map[int64]*db.WorkflowStep, len(existing))
	for i := range existing {
		byID[existing[i].ID] = &existing[i]
	}

	// Start from what is stored, then overlay. "" is a real value here: it
	// clears, which a missing key does not.
	stepLevel := map[int64]*string{}
	perLang := map[int64]map[string]string{}
	for _, s := range existing {
		perLang[s.ID] = map[string]string{}
		for lang, ts := range current.Languages(s.ID) {
			perLang[s.ID][lang] = ts.Time.UTC().Format(time.RFC3339)
		}
	}

	for _, p := range patch {
		step, ok := byID[p.WorkflowStepID]
		if !ok {
			return nil, invalidf("workflow step %d is not part of this project — list them with openloc_list_project_workflow_steps", p.WorkflowStepID)
		}
		if stepdef.IsAutomated(step.StepType) {
			return nil, invalidf("step %q runs by itself and cannot have a deadline", step.Name)
		}
		if lang := langcode.Normalize(p.TargetLanguage); lang != "" {
			if p.Deadline == "" {
				delete(perLang[step.ID], lang)
			} else {
				perLang[step.ID][lang] = p.Deadline
			}
			continue
		}
		d := p.Deadline
		stepLevel[step.ID] = &d
	}

	out := make([]settings.StepInput, 0, len(existing))
	for i := range existing {
		s := &existing[i]
		in := settings.StepInput{DefaultAssignee: s.DefaultAssignee}
		if s.StepDefinitionID != nil {
			in.StepDefinitionID = s.StepDefinitionID
		} else {
			in.ExistingStepID = &s.ID
		}
		if d, ok := stepLevel[s.ID]; ok {
			in.Deadline = d
		} else if s.Deadline.Valid {
			d := s.Deadline.Time.UTC().Format(time.RFC3339)
			in.Deadline = &d
		}
		for lang, d := range perLang[s.ID] {
			dv := d
			in.LanguageDeadlines = append(in.LanguageDeadlines, settings.LanguageDeadlineInput{
				TargetLanguage: lang, Deadline: &dv,
			})
		}
		out = append(out, in)
	}
	return out, nil
}

// workflowStepSummaries is the read shape both tools answer with, so a write
// reports exactly what a follow-up read would show.
func (b *directBackend) workflowStepSummaries(ctx context.Context, tenantID, profileID int64) ([]ProjectWorkflowStepSummary, error) {
	steps, err := settings.ListStepsWithDeadlines(ctx, b.q, profileID)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectWorkflowStepSummary, 0, len(steps))
	for _, s := range steps {
		item := ProjectWorkflowStepSummary{
			WorkflowStepID: s.ID, Name: s.Name, StepOrder: s.StepOrder, StepType: s.StepType,
			LanguageDeadlines: make([]StepLanguageDeadline, 0, len(s.LanguageDeadlines)),
		}
		if s.Deadline.Valid {
			item.Deadline = s.Deadline.Time.UTC().Format(time.RFC3339)
		}
		for _, ld := range s.LanguageDeadlines {
			item.LanguageDeadlines = append(item.LanguageDeadlines, StepLanguageDeadline(ld))
		}
		if s.DefaultAssignee != nil {
			if u, err := b.q.GetUser(ctx, *s.DefaultAssignee); err == nil {
				item.AssigneeName = u.Name
			}
		}
		out = append(out, item)
	}
	return out, nil
}
