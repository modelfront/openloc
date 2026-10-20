package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/job"
	"openloc/internal/label"
	"openloc/internal/pipeline"
	"openloc/internal/project"
	"openloc/internal/projectevents"
	"openloc/internal/ptr"
	"openloc/internal/settings"
)

// Project, file and label tools. Every
// method calls the service its REST handler calls; this file only maps
// arguments in and results out.

// ProjectOpsServices are the REST service objects the project and file tools
// share with their handlers, so a tool cannot drift from its route. Built in
// api.go next to the handlers they back.
type ProjectOpsServices struct {
	Projects *project.Handler
	Files    *file.Handler
	Jobs     *job.Handler
	// Exports writes a file's translation, for an export returned as content.
	Exports *pipeline.Service
}

// WireProjectOps hands the services to a backend built by
// NewDirectBackendWithPool. Call before serving; a backend without them
// answers those tools with a model-visible error.
func WireProjectOps(b Backend, s ProjectOpsServices) {
	if d, ok := b.(*directBackend); ok {
		d.ops = s
	}
}

func (b *directBackend) projects() (*project.Handler, error) {
	if b.ops.Projects == nil {
		return nil, invalidf("project management is not available on this server")
	}
	return b.ops.Projects, nil
}

func (b *directBackend) GetProjectSettings(ctx context.Context, in ProjectRef) (ProjectSettingsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return ProjectSettingsOut{}, err
	}
	tenantID, _ := scope(ctx)
	detail, err := ph.Settings(ctx, tenantID, in.ProjectID)
	if err != nil {
		return ProjectSettingsOut{}, serviceErr(err, "get project settings")
	}
	picks, err := label.ProjectPicks(ctx, b.q, tenantID, in.ProjectID)
	if err != nil {
		return ProjectSettingsOut{}, err
	}
	s := detail.Project
	out := ProjectSettingsOut{
		ProjectID:             s.ID,
		FuzzyThreshold:        s.FuzzyThreshold,
		SublanguagePenaltyPct: s.SublanguagePenaltyPct,
		NearExactPenaltyPct:   s.NearExactPenaltyPct,
		TMOnUpload:            s.TmOnUpload,
		MTOnUpload:            s.MtOnUpload,
		AutoComplete:          s.AutoComplete,
		InlineHTML:            s.InlineHTML,
		QAProfileID:           s.QaProfileID,
		MTRouterID:            s.MTRouterID,
		MTRouterName:          ptr.Deref(s.MTRouterName),
		WorkflowID:            s.WorkflowID,
		WorkflowName:          ptr.Deref(s.WorkflowName),
		TMs:                   tmAssignmentsOut(detail.TMAssignments),
		TermBases:             termAssignmentsOut(detail.TermAssignments),
		WorkflowSteps:         workflowStepsOut(plainSteps(detail.WorkflowSteps)),
		Labels:                labelPicksOut(picks),
	}
	if l := detail.TMProfile; l != nil {
		out.TMProfile = &TMProfileOut{ID: l.ID, Name: l.Name, UsageCount: l.UsageCount}
	}
	return out, nil
}

func tmAssignmentsOut(rows []db.SettingsTmAssignment) []TMAssignmentOut {
	out := make([]TMAssignmentOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, TMAssignmentOut{TMID: r.TmID, Mode: r.Mode, Priority: r.Priority, PenaltyPct: r.PenaltyPct})
	}
	return out
}

func termAssignmentsOut(rows []db.SettingsTerminologyAssignment) []TermAssignmentOut {
	out := make([]TermAssignmentOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, TermAssignmentOut{
			TermBaseID: r.TermBaseID, SourceLanguage: r.SourceLanguage, TargetLanguage: r.TargetLanguage,
			Mode: r.Mode, Priority: r.Priority,
		})
	}
	return out
}

// plainSteps drops the per-language deadlines, which the deadline tools report.
func plainSteps(rows []settings.StepWithDeadlines) []db.WorkflowStep {
	out := make([]db.WorkflowStep, len(rows))
	for i, r := range rows {
		out[i] = r.WorkflowStep
	}
	return out
}

func workflowStepsOut(rows []db.WorkflowStep) []WorkflowStepOut {
	out := make([]WorkflowStepOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkflowStepOut{
			ID: r.ID, Position: r.StepOrder, Name: r.Name, StepType: r.StepType, RoleRequired: r.RoleRequired,
			StepDefinitionID: r.StepDefinitionID, DefaultAssignee: r.DefaultAssignee, AIRouterID: r.AiRouterID,
			Deadline: tsString(r.Deadline),
		})
	}
	return out
}

func labelPicksOut(picks []label.Pick) []LabelPickOut {
	out := make([]LabelPickOut, 0, len(picks))
	for _, p := range picks {
		out = append(out, LabelPickOut{LabelID: p.LabelDefinitionID, LabelName: p.LabelName, ValueID: p.LabelValueID, LabelValue: p.LabelValue})
	}
	return out
}

func (b *directBackend) ListProjectEvents(ctx context.Context, in ListProjectEventsIn) (ProjectEventsOut, error) {
	tenantID, _ := scope(ctx)
	page, err := projectevents.List(ctx, b.q, tenantID, in.ProjectID, projectevents.ListParams{
		Cursor: in.Cursor, Limit: int32(clampLimit(in.Limit)), ActorID: in.ActorID, TypePrefix: in.Type,
	})
	if err != nil {
		return ProjectEventsOut{}, serviceErr(err, "list project events")
	}
	out := ProjectEventsOut{Events: make([]ProjectEventOut, 0, len(page.Events)), HasMore: page.HasMore, NextCursor: page.NextCursor}
	for _, e := range page.Events {
		item := ProjectEventOut{ID: e.ID, EventType: e.EventType, FileID: e.FileID, JobID: e.JobID, CreatedAt: e.CreatedAt}
		if e.Actor != nil {
			item.ActorName = e.Actor.Name
		}
		if len(e.Details) > 0 {
			if err := json.Unmarshal(e.Details, &item.Details); err != nil {
				return ProjectEventsOut{}, fmt.Errorf("decode event %d details: %w", e.ID, err)
			}
			item.Details = eventDetails(item.Details)
		}
		out.Events = append(out.Events, item)
	}
	return out, nil
}

func (b *directBackend) UpdateProject(ctx context.Context, in UpdateProjectIn) (ProjectDetail, error) {
	tenantID, _ := scope(ctx)
	p, err := project.UpdateMetadata(ctx, b.q, tenantID, in.ProjectID, project.MetadataPatch{
		Name: in.Name, Description: in.Description, Status: in.Status, Deadline: in.Deadline,
	})
	if err != nil {
		return ProjectDetail{}, serviceErr(err, "update project")
	}
	return b.projectDetail(ctx, p)
}

func (b *directBackend) SetProjectLabels(ctx context.Context, in SetProjectLabelsIn) (ProjectLabelsOut, error) {
	if err := b.needPool("changing a project's labels"); err != nil {
		return ProjectLabelsOut{}, err
	}
	tenantID, _ := scope(ctx)
	picks, err := label.SetProjectPicks(ctx, b.pool, b.q, tenantID, in.ProjectID, in.LabelValueIDs)
	if err != nil {
		return ProjectLabelsOut{}, serviceErr(err, "set project labels")
	}
	return ProjectLabelsOut{Labels: labelPicksOut(picks)}, nil
}

func (b *directBackend) UpdateProjectSettings(ctx context.Context, in UpdateProjectSettingsIn) (ProjectSettingsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return ProjectSettingsOut{}, err
	}
	tenantID, _ := scope(ctx)
	if _, err := ph.UpdateSettings(ctx, tenantID, in.ProjectID, project.SettingsPatch{
		FuzzyThreshold:        in.FuzzyThreshold,
		MtOnUpload:            in.MTOnUpload,
		TmOnUpload:            in.TMOnUpload,
		SublanguagePenaltyPct: in.SublanguagePenaltyPct,
		NearExactPenaltyPct:   in.NearExactPenaltyPct,
		AutoComplete:          in.AutoComplete,
		InlineHTML:            in.InlineHTML,
		QAProfileID:           in.QAProfileID,
		MTRouterID:            in.MTRouterID,
		TMProfileID:           in.TMProfileID,
	}); err != nil {
		return ProjectSettingsOut{}, serviceErr(err, "update project settings")
	}
	return b.GetProjectSettings(ctx, ProjectRef{ProjectID: in.ProjectID})
}

func (b *directBackend) SetProjectTMs(ctx context.Context, in SetProjectTMsIn) (ProjectTMsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return ProjectTMsOut{}, err
	}
	tenantID, _ := scope(ctx)
	inputs := make([]settings.TMAssignmentInput, 0, len(in.TMs))
	for _, t := range in.TMs {
		inputs = append(inputs, settings.TMAssignmentInput{TmID: t.TMID, Mode: t.Mode, Priority: t.Priority, PenaltyPct: t.PenaltyPct})
	}
	rows, err := ph.SetTMAssignments(ctx, tenantID, in.ProjectID, inputs)
	if err != nil {
		return ProjectTMsOut{}, serviceErr(err, "set project TMs")
	}
	return ProjectTMsOut{TMs: tmAssignmentsOut(rows)}, nil
}

func (b *directBackend) SetProjectTermBases(ctx context.Context, in SetProjectTermBasesIn) (ProjectTermBasesOut, error) {
	ph, err := b.projects()
	if err != nil {
		return ProjectTermBasesOut{}, err
	}
	tenantID, _ := scope(ctx)
	inputs := make([]settings.TermAssignmentInput, 0, len(in.TermBases))
	for _, t := range in.TermBases {
		inputs = append(inputs, settings.TermAssignmentInput{
			TermBaseID: t.TermBaseID, SourceLanguage: t.SourceLanguage, TargetLanguage: t.TargetLanguage,
			Mode: t.Mode, Priority: t.Priority,
		})
	}
	rows, err := ph.SetTermAssignments(ctx, tenantID, in.ProjectID, inputs)
	if err != nil {
		return ProjectTermBasesOut{}, serviceErr(err, "set project term bases")
	}
	return ProjectTermBasesOut{TermBases: termAssignmentsOut(rows)}, nil
}

// stepInputs maps the tool's steps onto the settings input. A step that
// leaves language_deadlines out keeps the dates of the step it stands for:
// the one it names by existing_step_id, or the current step at its position
// when both come from the same library step.
func (b *directBackend) stepInputs(ctx context.Context, p db.Project, existing []db.WorkflowStep, steps []WorkflowStepIn) ([]settings.StepInput, error) {
	current, err := settings.LoadStepDeadlines(ctx, b.q, p.SettingsProfileID)
	if err != nil {
		return nil, fmt.Errorf("load step deadlines: %w", err)
	}
	out := make([]settings.StepInput, 0, len(steps))
	for i, s := range steps {
		in := settings.StepInput{
			StepDefinitionID: s.StepDefinitionID, ExistingStepID: s.ExistingStepID,
			DefaultAssignee: s.DefaultAssignee, Deadline: s.Deadline, AIRouterID: s.AIRouterID,
		}
		switch {
		case s.LanguageDeadlines != nil:
			for _, ld := range *s.LanguageDeadlines {
				d := ld.Deadline
				in.LanguageDeadlines = append(in.LanguageDeadlines, settings.LanguageDeadlineInput{TargetLanguage: ld.TargetLanguage, Deadline: &d})
			}
		case s.ExistingStepID != nil:
			in.LanguageDeadlines = keptLanguageDeadlines(current, *s.ExistingStepID)
		case i < len(existing) && s.StepDefinitionID != nil && existing[i].StepDefinitionID != nil && *existing[i].StepDefinitionID == *s.StepDefinitionID:
			in.LanguageDeadlines = keptLanguageDeadlines(current, existing[i].ID)
		}
		out = append(out, in)
	}
	return out, nil
}

// keptLanguageDeadlines is a step's stored per-language dates as input, in
// language order.
func keptLanguageDeadlines(current settings.StepDeadlines, stepID int64) []settings.LanguageDeadlineInput {
	stored := current.Languages(stepID)
	langs := make([]string, 0, len(stored))
	for lang := range stored {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	out := make([]settings.LanguageDeadlineInput, 0, len(langs))
	for _, lang := range langs {
		d := stored[lang].Time.UTC().Format(time.RFC3339)
		out = append(out, settings.LanguageDeadlineInput{TargetLanguage: lang, Deadline: &d})
	}
	return out
}

// workflowImpact names what a workflow change reaches: the project's jobs
// that are still open take changed assignees, and a step appended past where
// a job stands starts on it.
func (b *directBackend) workflowImpact(ctx context.Context, p db.Project) ([]string, error) {
	// ListJobsByProject is project-scoped; p came from the tenant-scoped lookup.
	jobs, err := b.q.ListJobsByProject(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	open := 0
	for _, j := range jobs {
		if j.Status != "completed" {
			open++
		}
	}
	if open == 0 {
		return []string{"No open jobs: only jobs created from now on use the new workflow."}, nil
	}
	return []string{
		fmt.Sprintf("%s in progress: a position a job has reached must keep its step, changed assignees move onto them, and a step added after a finished position starts right away (an AI step runs, and may cost money).", plural(open, "job is", "jobs are")),
	}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (b *directBackend) PreviewSetProjectWorkflowSteps(ctx context.Context, in SetProjectWorkflowStepsIn) (Preview, error) {
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return Preview{}, err
	}
	existing, err := b.q.ListSettingsWorkflowSteps(ctx, p.SettingsProfileID)
	if err != nil {
		return Preview{}, fmt.Errorf("list workflow steps: %w", err)
	}
	tenantID, _ := scope(ctx)
	inputs, err := b.stepInputs(ctx, p, existing, in.Steps)
	if err != nil {
		return Preview{}, err
	}
	if err := settings.ValidateStepInputs(ctx, b.q, tenantID, existing, inputs); err != nil {
		return Preview{}, serviceErr(err, "validate workflow steps")
	}
	effects, err := b.workflowImpact(ctx, p)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Replace the workflow of project “%s” with %s.", p.Name, plural(len(in.Steps), "step", "steps")),
		Effects: effects,
	}, nil
}

func (b *directBackend) SetProjectWorkflowSteps(ctx context.Context, in SetProjectWorkflowStepsIn) (WorkflowStepsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return WorkflowStepsOut{}, err
	}
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return WorkflowStepsOut{}, err
	}
	existing, err := b.q.ListSettingsWorkflowSteps(ctx, p.SettingsProfileID)
	if err != nil {
		return WorkflowStepsOut{}, fmt.Errorf("list workflow steps: %w", err)
	}
	inputs, err := b.stepInputs(ctx, p, existing, in.Steps)
	if err != nil {
		return WorkflowStepsOut{}, err
	}
	tenantID, _ := scope(ctx)
	steps, err := ph.SetWorkflowSteps(ctx, tenantID, in.ProjectID, inputs)
	if err != nil {
		return WorkflowStepsOut{}, serviceErr(err, "set workflow steps")
	}
	return WorkflowStepsOut{Steps: workflowStepsOut(steps)}, nil
}

func (b *directBackend) PreviewApplyWorkflow(ctx context.Context, in ApplyWorkflowIn) (Preview, error) {
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return Preview{}, err
	}
	tenantID, _ := scope(ctx)
	wf, err := b.q.GetWorkflow(ctx, db.GetWorkflowParams{ID: in.WorkflowID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Preview{}, invalidf("workflow not found in this tenant")
		}
		return Preview{}, fmt.Errorf("get workflow: %w", err)
	}
	effects, err := b.workflowImpact(ctx, p)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Replace the workflow of project “%s” with a copy of the library workflow “%s”.", p.Name, wf.Name),
		Effects: effects,
	}, nil
}

func (b *directBackend) ApplyWorkflow(ctx context.Context, in ApplyWorkflowIn) (WorkflowStepsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return WorkflowStepsOut{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := ph.ApplyWorkflow(ctx, tenantID, in.ProjectID, in.WorkflowID)
	if err != nil {
		return WorkflowStepsOut{}, serviceErr(err, "apply workflow")
	}
	return WorkflowStepsOut{WorkflowID: &res.WorkflowID, WorkflowName: ptr.Deref(res.WorkflowName), Steps: workflowStepsOut(res.Steps)}, nil
}

func (b *directBackend) PublishProject(ctx context.Context, in PublishProjectIn) (ProjectDetail, error) {
	ph, err := b.projects()
	if err != nil {
		return ProjectDetail{}, err
	}
	tenantID, userID := scope(ctx)
	p, err := ph.Publish(ctx, tenantID, userID, in.ProjectID, in.Analyze)
	if err != nil {
		if errors.Is(err, project.ErrPublishFiles) {
			return ProjectDetail{}, invalidf("%s", project.ErrPublishFiles.Error())
		}
		return ProjectDetail{}, serviceErr(err, "publish project")
	}
	return b.projectDetail(ctx, p)
}

func (b *directBackend) PretranslateProject(ctx context.Context, in ProjectRef) (PretranslateOut, error) {
	if b.ops.Jobs == nil {
		return PretranslateOut{}, invalidf("pre-translation is not available on this server")
	}
	tenantID, _ := scope(ctx)
	res, err := b.ops.Jobs.PretranslateProject(ctx, tenantID, in.ProjectID)
	if err != nil {
		return PretranslateOut{}, serviceErr(err, "pretranslate project")
	}
	return PretranslateOut{Queued: res.Queued, Skipped: res.Skipped, Total: res.Total}, nil
}

func (b *directBackend) PreviewClearProjectTranslations(ctx context.Context, in ClearProjectTranslationsIn) (Preview, error) {
	ph, err := b.projects()
	if err != nil {
		return Preview{}, err
	}
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return Preview{}, err
	}
	n, err := ph.PreviewClearTranslations(ctx, p.TenantID, p.ID, project.ClearScope(in.Scope))
	if err != nil {
		return Preview{}, serviceErr(err, "preview clear")
	}
	what := "translations that are not confirmed"
	if in.Scope == string(project.ClearAll) {
		what = "all translations, confirmed ones included"
	}
	return Preview{
		Summary: fmt.Sprintf("Clear %s in project “%s”.", what, p.Name),
		Effects: []string{
			fmt.Sprintf("%s emptied, with their QA findings and AI comments. Locked segments, and segments another user has open, are kept.", plural(int(n), "segment translation is", "segment translations are")),
		},
	}, nil
}

func (b *directBackend) ClearProjectTranslations(ctx context.Context, in ClearProjectTranslationsIn) (ClearTranslationsOut, error) {
	ph, err := b.projects()
	if err != nil {
		return ClearTranslationsOut{}, err
	}
	tenantID, _ := scope(ctx)
	res, err := ph.ClearTranslations(ctx, tenantID, in.ProjectID, project.ClearScope(in.Scope))
	if err != nil {
		return ClearTranslationsOut{}, serviceErr(err, "clear translations")
	}
	return ClearTranslationsOut{Cleared: res.Cleared, Held: res.Held}, nil
}
