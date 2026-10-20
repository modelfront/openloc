package project

// create_setup.go — project creation with initial configuration.
// CreateWithSetup applies the wizard's whole
// initial configuration server-side and records ONE project.created event
// carrying the full setup.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/systemworkflow"
)

// SetupConfig is the optional initial configuration applied right after
// creation; the shape is shared with template creation (settings.Setup).
type SetupConfig = settings.Setup

// CreateWithSetup creates a project, optionally applies a template, applies
// the initial configuration, and records a single project.created audit event
// describing all of it, in one transaction: a setup refused at any step
// leaves no project behind. Setup inputs that can be checked up front are
// validated before the transaction opens.
func CreateWithSetup(ctx context.Context, pool *pgxpool.Pool, q db.Querier, tenantID, userID int64, p CreateParams, templateID *int64, cfg SetupConfig) (db.Project, error) {
	if err := settings.ValidateSetup(ctx, q, tenantID, cfg); err != nil {
		return db.Project{}, err
	}
	if templateID != nil {
		if _, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: *templateID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.Project{}, validationf("template not found in this tenant")
			}
			return db.Project{}, fmt.Errorf("get template: %w", err)
		}
	}

	if p.Status != string(db.ProjectStatusDraft) {
		if err := checkSetupRouter(ctx, q, tenantID, templateID, cfg); err != nil {
			return db.Project{}, err
		}
	}

	var project db.Project
	err := database.InTx(ctx, pool, func(tx pgx.Tx, q db.Querier) error {
		var err error
		if project, err = createCore(ctx, q, tenantID, userID, p); err != nil {
			return err
		}
		extra := map[string]any{}
		if templateID != nil {
			if project, _, err = ApplyTemplate(ctx, tx, q, tenantID, project.ID, *templateID); err != nil {
				return fmt.Errorf("template %d not applied: %w", *templateID, err)
			}
			for k, v := range templateDetails(ctx, q, tenantID, *templateID) {
				extra[k] = v
			}
		}
		if err := applySetup(ctx, tx, q, tenantID, project, cfg, extra); err != nil {
			return err
		}
		return emitCreated(ctx, q, project, tenantID, userID, extra)
	})
	if err != nil {
		return db.Project{}, err
	}
	return project, nil
}

// checkSetupRouter refuses a live create whose setup ends Pre-translation
// without an MT router. The steps come from the explicit list, else the
// workflow, else the template; the router from the setup, else the template.
func checkSetupRouter(ctx context.Context, q db.Querier, tenantID int64, templateID *int64, cfg SetupConfig) error {
	var tmplProfile *db.SettingsProfile
	if templateID != nil {
		tmpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: *templateID, TenantID: tenantID})
		if err != nil {
			return fmt.Errorf("get template: %w", err)
		}
		prof, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
		if err != nil {
			return fmt.Errorf("get template settings: %w", err)
		}
		tmplProfile = &prof
	}

	var only bool
	switch {
	case len(cfg.WorkflowSteps) > 0:
		var err error
		if only, err = settings.StepInputsArePretranslateOnly(ctx, q, tenantID, cfg.WorkflowSteps); err != nil {
			return err
		}
	case cfg.WorkflowID != nil:
		wf, err := q.GetApplicableWorkflow(ctx, db.GetApplicableWorkflowParams{ID: *cfg.WorkflowID, TenantID: tenantID})
		if err != nil {
			return fmt.Errorf("get workflow: %w", err)
		}
		only = wf.SystemKey != nil && *wf.SystemKey == systemworkflow.WorkflowKey
	case tmplProfile != nil:
		steps, err := q.ListSettingsWorkflowSteps(ctx, tmplProfile.ID)
		if err != nil {
			return fmt.Errorf("list template steps: %w", err)
		}
		only = settings.IsPretranslateOnly(steps)
	}
	if !only {
		return nil
	}
	if cfg.MTRouterID != nil || (tmplProfile != nil && tmplProfile.MtRouterID != nil) {
		return nil
	}
	return settings.ErrPretranslateNeedsRouter
}

// applySetup applies an initial configuration to an existing project and adds
// the resulting settings + assignment snapshot to extra (the details of the
// project.created event). Shared by creation and by draft autosave, which
// replays the wizard's whole form on every save.
func applySetup(ctx context.Context, pool database.TxBeginner, q db.Querier, tenantID int64, project db.Project, cfg SetupConfig, extra map[string]any) error {
	if err := settings.ApplySetup(ctx, pool, q, tenantID, project.SettingsProfileID, cfg); err != nil {
		return err
	}
	if cfg.WorkflowID != nil {
		extra["workflow_id"] = *cfg.WorkflowID
		if wf, err := q.GetWorkflowAny(ctx, db.GetWorkflowAnyParams{ID: *cfg.WorkflowID, TenantID: tenantID}); err == nil {
			extra["workflow_name"] = wf.Name
		}
	}

	snapshotSetup(ctx, q, tenantID, project.SettingsProfileID, extra)
	return nil
}

// snapshotSetup adds the profile's current settings + assignments to extra —
// the "how the project started" half of the project.created event.
func snapshotSetup(ctx context.Context, q db.Querier, tenantID, profileID int64, extra map[string]any) {
	extra["settings"] = finalSettingsDetails(ctx, q, profileID)
	if tms, err := q.ListSettingsTMAssignments(ctx, profileID); err == nil && len(tms) > 0 {
		extra["tm_assignments"] = tmAssignmentsPayload(ctx, q, tenantID, tms)
	}
	if terms, err := q.ListSettingsTermAssignments(ctx, profileID); err == nil && len(terms) > 0 {
		extra["term_assignments"] = termAssignmentsPayload(ctx, q, tenantID, terms)
	}
	if steps, err := q.ListSettingsWorkflowSteps(ctx, profileID); err == nil && len(steps) > 0 {
		extra["workflow_steps"] = workflowStepsPayload(ctx, q, tenantID, steps)
	}
}

func finalSettingsDetails(ctx context.Context, q db.Querier, profileID int64) map[string]any {
	profile, err := q.GetSettingsProfile(ctx, profileID)
	if err != nil {
		return nil
	}
	return settingsMap(profile)
}
