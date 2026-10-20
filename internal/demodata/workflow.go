package demodata

import (
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
)

// stepSpec is one workflow step on a project's settings profile.
type stepSpec struct {
	name     string
	stepType string
	assignee *int64
	def      *db.StepDefinition
	// deadlineDays is the step's due date in days from now; 0 leaves it unset.
	deadlineDays int
}

// workflow is Translation → Review, both due a few days out. The first target
// language is due a day before the Translation step, so the Due dates section
// shows a step date plus one language on its own rather than empty fields. In
// organization mode both steps link to the library definitions and the profile
// is based on the Standard workflow.
func (b *builder) workflow(p db.Project, translator, reviewer int64) ([]db.WorkflowStep, error) {
	specs := []stepSpec{
		{name: StepTranslation, assignee: &translator, deadlineDays: 4},
		{name: StepReview, assignee: &reviewer, deadlineDays: 6},
	}
	var wf *int64
	if b.org != nil {
		specs[0].def, specs[1].def = &b.lib.Translation, &b.lib.Review
		wf = &b.lib.Standard.ID
	}
	steps, err := b.steps(p.SettingsProfileID, specs, wf)
	if err != nil {
		return nil, err
	}
	if len(p.TargetLanguages) > 0 {
		if err := b.q.CreateWorkflowStepAssignment(b.ctx, db.CreateWorkflowStepAssignmentParams{
			WorkflowStepID: steps[0].ID, TargetLanguage: langcode.Normalize(p.TargetLanguages[0]), Deadline: b.deadline(3),
		}); err != nil {
			return nil, fmt.Errorf("step language deadline: %w", err)
		}
	}
	return steps, nil
}

// reviewWorkflow is the regulated-content workflow. In organization mode it is
// the library's "Human + AI review": Translation → AI review, a hybrid step
// whose AI pass has run and whose human pass is the owner's. Otherwise the
// plain Translation → Review.
func (b *builder) reviewWorkflow(p db.Project) ([]db.WorkflowStep, error) {
	if b.org == nil || !b.lib.HasAI {
		return b.workflow(p, b.linguistID, b.ownerID)
	}
	specs := []stepSpec{
		{name: StepTranslation, assignee: &b.linguistID, def: &b.lib.Translation},
		{name: StepAIReview, stepType: b.lib.AIReview.StepType, assignee: &b.ownerID, def: &b.lib.AIReview},
	}
	return b.steps(p.SettingsProfileID, specs, &b.lib.HumanAI.ID)
}

func (b *builder) steps(profileID int64, specs []stepSpec, workflowID *int64) ([]db.WorkflowStep, error) {
	out := make([]db.WorkflowStep, 0, len(specs))
	for i, s := range specs {
		params := db.CreateSettingsWorkflowStepParams{SettingsProfileID: profileID, Name: s.name, StepOrder: int32(i), RoleRequired: "linguist", DefaultAssignee: s.assignee, StepType: s.stepType}
		if s.deadlineDays != 0 {
			params.Deadline = b.deadline(s.deadlineDays)
		}
		if s.def != nil {
			params.StepDefinitionID = &s.def.ID
			params.AiRouterID = s.def.AiRouterID
		}
		st, err := b.q.CreateSettingsWorkflowStep(b.ctx, params)
		if err != nil {
			return nil, fmt.Errorf("%s step: %w", s.name, err)
		}
		out = append(out, st)
	}
	if workflowID != nil {
		if err := b.q.SetSettingsProfileWorkflow(b.ctx, db.SetSettingsProfileWorkflowParams{ID: profileID, WorkflowID: workflowID}); err != nil {
			return nil, fmt.Errorf("profile workflow: %w", err)
		}
	}
	return out, nil
}
