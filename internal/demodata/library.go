package demodata

// The step-definition + workflow library and the MT / AI
// routers every sample organization uses. Shared by `make seed` and the demo
// organization; each item is looked up by name before it is created, so a
// second call changes nothing.

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/stepdef"
	"openloc/internal/systemworkflow"
	"openloc/internal/workflowlib"
)

// Library names. The seed and the demo organization share them.
const (
	WorkflowStandard = "Standard"
	WorkflowAIFirst  = "AI-first"
	WorkflowHumanAI  = "Human + AI review"
	StepTranslation  = "Translation"
	StepReview       = "Review"
	StepAIReview     = "AI review"
	StepAutoAPE      = "Auto APE"
	AIRouterName     = "Default QE+APE router"
	MTRouterName     = "Default MT router"
)

// Library is the tenant's step definitions and workflows. The AI fields are
// zero when no QE+APE engine was given.
type Library struct {
	Translation, Review db.StepDefinition
	AIReview, AutoAPE   db.StepDefinition
	AIRouter            db.AiRouter
	Standard            db.Workflow
	AIFirst, HumanAI    db.Workflow
	HasAI               bool
}

// EnsureWorkflowLibrary gives the tenant the built-in Pre-translation
// workflow, the default Translation and Review definitions and the Standard
// workflow. With a QE+APE engine it adds an AI
// router defaulting to it, a hybrid "AI review" step, an ai "Auto APE" step and
// the two workflows built from them.
func EnsureWorkflowLibrary(ctx context.Context, q db.Querier, tenantID int64, qeapeEngineID *int64) (Library, error) {
	var lib Library
	if err := systemworkflow.EnsurePretranslate(ctx, q, tenantID); err != nil {
		return lib, err
	}
	if err := stepdef.EnsureDefaults(ctx, q, tenantID); err != nil {
		return lib, fmt.Errorf("default step definitions: %w", err)
	}
	var err error
	if lib.Translation, err = q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: StepTranslation}); err != nil {
		return lib, fmt.Errorf("step definition %q: %w", StepTranslation, err)
	}
	if lib.Review, err = q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: StepReview}); err != nil {
		return lib, fmt.Errorf("step definition %q: %w", StepReview, err)
	}
	if lib.Standard, err = ensureWorkflow(ctx, q, tenantID, WorkflowStandard, lib.Translation, lib.Review); err != nil {
		return lib, err
	}
	if qeapeEngineID == nil {
		return lib, nil
	}
	if lib.AIRouter, _, err = EnsureAIRouter(ctx, q, tenantID, AIRouterName, *qeapeEngineID); err != nil {
		return lib, err
	}
	if lib.AIReview, err = ensureStepDefinition(ctx, q, tenantID, StepAIReview, stepdef.TypeHybrid, &lib.AIRouter.ID); err != nil {
		return lib, err
	}
	if lib.AutoAPE, err = ensureStepDefinition(ctx, q, tenantID, StepAutoAPE, stepdef.TypeAI, &lib.AIRouter.ID); err != nil {
		return lib, err
	}
	if lib.AIFirst, err = ensureWorkflow(ctx, q, tenantID, WorkflowAIFirst, lib.AutoAPE, lib.Review); err != nil {
		return lib, err
	}
	if lib.HumanAI, err = ensureWorkflow(ctx, q, tenantID, WorkflowHumanAI, lib.Translation, lib.AIReview); err != nil {
		return lib, err
	}
	lib.HasAI = true
	return lib, nil
}

func ensureStepDefinition(ctx context.Context, q db.Querier, tenantID int64, name, stepType string, router *int64) (db.StepDefinition, error) {
	if d, err := q.GetStepDefinitionByName(ctx, db.GetStepDefinitionByNameParams{TenantID: tenantID, Name: name}); err == nil {
		return d, nil
	} else if !isNoRows(err) {
		return db.StepDefinition{}, fmt.Errorf("step definition %q: %w", name, err)
	}
	d, err := q.CreateStepDefinition(ctx, db.CreateStepDefinitionParams{
		TenantID: tenantID, Name: name, StepType: stepType, RoleRequired: "linguist", AiRouterID: router,
	})
	if err != nil {
		return db.StepDefinition{}, fmt.Errorf("create step definition %q: %w", name, err)
	}
	return d, nil
}

func ensureWorkflow(ctx context.Context, q db.Querier, tenantID int64, name string, defs ...db.StepDefinition) (db.Workflow, error) {
	existing, err := q.ListWorkflows(ctx, tenantID)
	if err != nil {
		return db.Workflow{}, fmt.Errorf("list workflows: %w", err)
	}
	for _, wf := range existing {
		if wf.Name == name {
			return wf, nil
		}
	}
	in := workflowlib.Input{Name: name}
	for _, d := range defs {
		in.Steps = append(in.Steps, workflowlib.StepInput{StepDefinitionID: d.ID})
	}
	wf, err := workflowlib.Create(ctx, q, tenantID, in)
	if err != nil {
		return db.Workflow{}, fmt.Errorf("create workflow %q: %w", name, err)
	}
	return wf, nil
}

// EnsureAIRouter returns the tenant's AI router called name, creating it with
// defaultEngine as its default when missing. created reports which.
func EnsureAIRouter(ctx context.Context, q db.Querier, tenantID int64, name string, defaultEngine int64) (db.AiRouter, bool, error) {
	routers, err := q.ListAIRouters(ctx, tenantID)
	if err != nil {
		return db.AiRouter{}, false, fmt.Errorf("list ai routers: %w", err)
	}
	for _, r := range routers {
		if r.Name == name {
			return r, false, nil
		}
	}
	r, err := q.CreateAIRouter(ctx, db.CreateAIRouterParams{TenantID: tenantID, Name: name, DefaultEngineID: &defaultEngine})
	if err != nil {
		return db.AiRouter{}, false, fmt.Errorf("create ai router %q: %w", name, err)
	}
	return r, true, nil
}

// EnsureMTRouter returns the tenant's MT router called name, creating it with
// mtEngineID as its default when missing. created reports which.
func EnsureMTRouter(ctx context.Context, q db.Querier, tenantID int64, name string, mtEngineID int64) (db.MtRouter, bool, error) {
	routers, err := q.ListMTRouters(ctx, tenantID)
	if err != nil {
		return db.MtRouter{}, false, fmt.Errorf("list mt routers: %w", err)
	}
	for _, r := range routers {
		if r.Name == name {
			return r, false, nil
		}
	}
	r, err := q.CreateMTRouter(ctx, db.CreateMTRouterParams{TenantID: tenantID, Name: name, DefaultMtEngineID: &mtEngineID})
	if err != nil {
		return db.MtRouter{}, false, fmt.Errorf("create mt router %q: %w", name, err)
	}
	return r, true, nil
}
