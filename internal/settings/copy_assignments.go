package settings

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// CopyAssignments copies all assignment types (TM, MT, term, workflow) from
// one settings profile to another. Full-replace semantics: destination lists
// are deleted first. Must be called within a transaction. Shared by
// apply-template, save-as-template, and template duplication.
func CopyAssignments(ctx context.Context, q db.Querier, srcID, dstID int64) error {
	if err := CopyTMAssignments(ctx, q, srcID, dstID); err != nil {
		return err
	}

	// MT router: the pointer travels with the rest of the profile.
	src, err := q.GetSettingsProfile(ctx, srcID)
	if err != nil {
		return fmt.Errorf("get source profile: %w", err)
	}
	if err := q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{ID: dstID, MtRouterID: src.MtRouterID}); err != nil {
		return fmt.Errorf("copy MT router: %w", err)
	}

	// Terminology assignments
	if err := q.DeleteSettingsTermAssignments(ctx, dstID); err != nil {
		return fmt.Errorf("delete term assignments: %w", err)
	}
	termAssigns, err := q.ListSettingsTermAssignments(ctx, srcID)
	if err != nil {
		return fmt.Errorf("list term assignments: %w", err)
	}
	for _, a := range termAssigns {
		if _, err := q.CreateSettingsTermAssignment(ctx, db.CreateSettingsTermAssignmentParams{
			SettingsProfileID: dstID, TermBaseID: a.TermBaseID,
			SourceLanguage: a.SourceLanguage, TargetLanguage: a.TargetLanguage,
			Mode: a.Mode, Priority: a.Priority,
		}); err != nil {
			return fmt.Errorf("create term assignment: %w", err)
		}
	}

	// Workflow steps. Identity (name, type, engine, library provenance) must
	// survive the copy; Deadline deliberately does NOT — step deadlines are
	// per-project scheduling, not reusable settings.
	if err := q.DeleteSettingsWorkflowSteps(ctx, dstID); err != nil {
		return fmt.Errorf("delete workflow steps: %w", err)
	}
	wfSteps, err := q.ListSettingsWorkflowSteps(ctx, srcID)
	if err != nil {
		return fmt.Errorf("list workflow steps: %w", err)
	}
	for _, s := range wfSteps {
		if _, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: dstID, Name: s.Name, StepOrder: s.StepOrder,
			RoleRequired: s.RoleRequired, DefaultAssignee: s.DefaultAssignee,
			AiRouterID: s.AiRouterID, StepType: s.StepType, StepDefinitionID: s.StepDefinitionID,
		}); err != nil {
			return fmt.Errorf("create workflow step: %w", err)
		}
	}
	// The "Based on workflow …" pointer travels with the steps it describes.
	srcProfile, err := q.GetSettingsProfile(ctx, srcID)
	if err != nil {
		return fmt.Errorf("get source profile: %w", err)
	}
	if err := q.SetSettingsProfileWorkflow(ctx, db.SetSettingsProfileWorkflowParams{ID: dstID, WorkflowID: srcProfile.WorkflowID}); err != nil {
		return fmt.Errorf("copy workflow provenance: %w", err)
	}

	return nil
}

// CopyTMAssignments full-replace-copies the TM rows (tm_id, mode, priority,
// penalty) from one settings profile to another. Must run inside the caller's
// transaction. Used on its own by TM-profile detach and save-as-profile, which
// must not touch the destination's MT/terminology/workflow lists.
func CopyTMAssignments(ctx context.Context, q db.Querier, srcID, dstID int64) error {
	if err := q.DeleteSettingsTMAssignments(ctx, dstID); err != nil {
		return fmt.Errorf("delete TM assignments: %w", err)
	}
	tmAssigns, err := q.ListSettingsTMAssignments(ctx, srcID)
	if err != nil {
		return fmt.Errorf("list TM assignments: %w", err)
	}
	for _, a := range tmAssigns {
		if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: dstID, TmID: a.TmID,
			Mode: a.Mode, Priority: a.Priority, PenaltyPct: a.PenaltyPct,
		}); err != nil {
			return fmt.Errorf("create TM assignment: %w", err)
		}
	}
	return nil
}
