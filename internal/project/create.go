package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/label"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/projectevents"
	"openloc/internal/settings"
)

// ErrValidation marks caller-fixable input problems. Both consumers surface
// the wrapped message verbatim: the HTTP handler as a 400, the MCP backend as
// a model-visible invalid-argument result.
var ErrValidation = errors.New("invalid project input")

func validationf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// CreateParams is the service-level input for project creation, shared by the
// HTTP handler and the MCP create_project tool so both run the SAME validation
// and creation sequence.
type CreateParams struct {
	Name            string
	Description     string
	SourceLanguage  string
	TargetLanguages []string
	CustomerID      *int64
	Status          string
	Deadline        string // ISO 8601; empty = none
	Client          string
	Domain          string
	// LabelValueIDs are the project's label picks (at most one value per
	// label), written in the create's transaction.
	LabelValueIDs []int64
}

// Create validates and creates a project plus its settings profile and the
// mandatory default workflow step, and records the project.created audit
// event, in one transaction. Kept for callers with no follow-up setup (MCP
// create_project); the HTTP handler goes through CreateWithSetup so the
// wizard's settings + assignments fold into ONE audit event instead of a burst.
//
// Without a pool (the MCP backend built by NewDirectBackend) the writes run on
// q; that backend refuses the creates that need a template or workflow.
func Create(ctx context.Context, pool *pgxpool.Pool, q db.Querier, tenantID, userID int64, p CreateParams) (db.Project, error) {
	var project db.Project
	write := func(_ pgx.Tx, q db.Querier) error {
		var err error
		if project, err = createCore(ctx, q, tenantID, userID, p); err != nil {
			return err
		}
		return emitCreated(ctx, q, project, tenantID, userID, nil)
	}
	var err error
	if pool == nil {
		err = write(nil, q)
	} else {
		err = database.InTx(ctx, pool, write)
	}
	if err != nil {
		return db.Project{}, err
	}
	return project, nil
}

// createCore validates and creates the project row + settings profile + the
// mandatory default workflow step + the label picks, with NO audit event — callers
// emit exactly one project.created once their whole setup is done. Runs on the
// caller's transaction.
func createCore(ctx context.Context, q db.Querier, tenantID, userID int64, p CreateParams) (db.Project, error) {
	status := db.ProjectStatusInProgress
	if p.Status != "" {
		if !validProjectStatuses[p.Status] {
			return db.Project{}, validationf("%s", invalidStatusMessage)
		}
		status = db.ProjectStatus(p.Status)
	}

	meta, err := validateMetadata(ctx, q, tenantID, p, status == db.ProjectStatusDraft)
	if err != nil {
		return db.Project{}, err
	}

	profile, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID:       tenantID,
		FuzzyThreshold: 0.75,
		MtOnUpload:     true,
		TmOnUpload:     true,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("create settings profile: %w", err)
	}

	// Workflow steps are mandatory: every profile starts with the default
	// Translation → Review workflow, linked to the tenant's default step
	// definitions.
	if err := settings.CreateDefaultWorkflowSteps(ctx, q, tenantID, profile.ID); err != nil {
		return db.Project{}, fmt.Errorf("create default workflow steps: %w", err)
	}

	project, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name:              meta.Name,
		Description:       p.Description,
		SourceLanguage:    meta.SourceLanguage,
		TargetLanguages:   meta.TargetLanguages,
		TenantID:          tenantID,
		SettingsProfileID: profile.ID,
		CustomerID:        meta.CustomerID,
		Status:            status,
		CreatedBy:         auth.UserIDPtr(userID),
		Deadline:          meta.Deadline,
		Client:            meta.Client,
		Domain:            meta.Domain,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("create project: %w", err)
	}
	if len(p.LabelValueIDs) > 0 {
		if err := label.ReplaceProjectPicks(ctx, q, tenantID, project.ID, p.LabelValueIDs); err != nil {
			return db.Project{}, fmt.Errorf("set project labels: %w", err)
		}
	}
	return project, nil
}

// projectMetadata is CreateParams after normalization and validation, in the
// shape the INSERT / draft UPDATE take.
type projectMetadata struct {
	Name            string
	SourceLanguage  string
	TargetLanguages []string
	CustomerID      *int64
	Deadline        pgtype.Timestamptz
	Client, Domain  *string
}

// validateMetadata normalizes and checks a project's metadata. A draft may
// still be missing its name and languages (the wizard autosaves a half-filled
// form); every other status needs all three. The checks that hold for a
// partial language list — target ≠ source, no duplicates — run for drafts too.
func validateMetadata(ctx context.Context, q db.Querier, tenantID int64, p CreateParams, draft bool) (projectMetadata, error) {
	m := projectMetadata{
		Name:            strings.TrimSpace(p.Name),
		SourceLanguage:  langcode.Normalize(p.SourceLanguage),
		TargetLanguages: langcode.NormalizeAll(p.TargetLanguages),
	}
	if m.TargetLanguages == nil {
		m.TargetLanguages = []string{}
	}

	if !draft && (m.Name == "" || m.SourceLanguage == "" || len(m.TargetLanguages) == 0) {
		return m, validationf("name, source_language, and target_languages are required")
	}
	seen := make(map[string]bool, len(m.TargetLanguages))
	for _, tgt := range m.TargetLanguages {
		if tgt == m.SourceLanguage {
			return m, validationf("target language %q cannot be the same as source language", tgt)
		}
		if seen[tgt] {
			return m, validationf("duplicate target language %q", tgt)
		}
		seen[tgt] = true
	}

	if p.CustomerID != nil && *p.CustomerID != 0 {
		if _, err := q.GetCustomer(ctx, db.GetCustomerParams{ID: *p.CustomerID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return m, validationf("customer not found in this tenant")
			}
			return m, fmt.Errorf("verify customer: %w", err)
		}
		m.CustomerID = p.CustomerID
	}

	deadline, err := parseDeadline(p.Deadline)
	if err != nil {
		return m, validationf("invalid deadline (must be ISO 8601, e.g. 2026-05-08T17:00:00Z)")
	}
	m.Deadline = deadline

	if s := strings.TrimSpace(p.Client); s != "" {
		m.Client = &s
	}
	if s := strings.TrimSpace(p.Domain); s != "" {
		m.Domain = &s
	}
	return m, nil
}

// emitCreated records the single project.created audit event, in the
// create's transaction: a failed audit row fails the create. A draft gets it
// when the draft starts, so it comes before the draft's uploads in History;
// recordPublish fills in the configuration it goes live with.
// extra carries setup context (template, settings, assignments) merged into
// the details so the whole initial configuration is ONE event.
func emitCreated(ctx context.Context, q db.Querier, project db.Project, tenantID, userID int64, extra map[string]any) error {
	details := createdDetails(project, extra)
	if auth.PATInfoFromContext(ctx) != nil {
		details["via"] = "mcp"
	}
	if err := projectevents.EmitInTx(ctx, q, projectevents.Event{
		ProjectID: project.ID,
		TenantID:  tenantID,
		Type:      projectevents.TypeProjectCreated,
		// Same value that went into projects.created_by above — the column and
		// the audit event can't disagree about who created a project.
		ActorID:   auth.UserIDPtr(userID),
		ActorName: auth.UserEmailFromContext(ctx),
		Details:   details,
	}); err != nil {
		return fmt.Errorf("record project.created: %w", err)
	}
	return nil
}

func createdDetails(project db.Project, extra map[string]any) map[string]any {
	details := map[string]any{
		"name":             project.Name,
		"source_language":  project.SourceLanguage,
		"target_languages": project.TargetLanguages,
	}
	for k, v := range extra {
		details[k] = v
	}
	return details
}

// recordPublish fills the draft's project.created with the configuration it is
// published with. That event keeps naming whoever started the draft, the same
// person as projects.created_by; a publish by anyone else adds a
// project.published entry naming the publisher. Best-effort, like emitCreated.
func recordPublish(ctx context.Context, q db.Querier, project db.Project, tenantID, userID int64, extra map[string]any) {
	details := createdDetails(project, extra)
	found, err := projectevents.CompleteCreated(ctx, q, project.ID, tenantID, details)
	switch {
	case err != nil:
		logging.Warn(ctx, err, "project.created not completed at publish", "project-publish-created-event-failed",
			logging.ProjectID(project.ID))
	case !found:
		// The draft has no project.created yet: record it now, still naming
		// the starter.
		projectevents.Emit(ctx, q, projectevents.Event{
			ProjectID: project.ID,
			TenantID:  tenantID,
			Type:      projectevents.TypeProjectCreated,
			ActorID:   project.CreatedBy,
			Details:   details,
		})
	}
	if project.CreatedBy == nil || *project.CreatedBy != userID {
		projectevents.Emit(ctx, q, projectevents.Event{
			ProjectID: project.ID,
			TenantID:  tenantID,
			Type:      projectevents.TypeProjectPublished,
			ActorID:   auth.UserIDPtr(userID),
			ActorName: auth.UserEmailFromContext(ctx),
		})
	}
}

// CreateWithTemplate creates a project and applies a template's settings +
// assignments in one transaction (templates are create-time-only). Used by the
// MCP create_project tool; the HTTP handler goes through CreateWithSetup. A
// failed apply leaves no project behind.
func CreateWithTemplate(ctx context.Context, pool *pgxpool.Pool, q db.Querier, tenantID, userID int64, p CreateParams, templateID int64) (db.Project, error) {
	if _, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: templateID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Project{}, validationf("template not found in this tenant")
		}
		return db.Project{}, fmt.Errorf("get template: %w", err)
	}
	var applied db.Project
	err := database.InTx(ctx, pool, func(tx pgx.Tx, q db.Querier) error {
		project, err := createCore(ctx, q, tenantID, userID, p)
		if err != nil {
			return err
		}
		if applied, _, err = ApplyTemplate(ctx, tx, q, tenantID, project.ID, templateID); err != nil {
			return fmt.Errorf("template %d not applied: %w", templateID, err)
		}
		// Template application is create-time-only, so it is context of the
		// creation, not a separate change — one event with the template folded in.
		return emitCreated(ctx, q, applied, tenantID, userID, templateDetails(ctx, q, tenantID, templateID))
	})
	if err != nil {
		return db.Project{}, err
	}
	return applied, nil
}

// templateDetails resolves the template name for event details; best-effort.
func templateDetails(ctx context.Context, q db.Querier, tenantID, templateID int64) map[string]any {
	d := map[string]any{"template_id": templateID}
	if tmpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: templateID, TenantID: tenantID}); err == nil {
		d["template_name"] = tmpl.Name
	}
	return d
}

// ApplyTemplate copies a template's settings, and TM/MT/terminology/workflow
// assignments onto an existing project, transactionally. Called at creation
// only (CreateWithTemplate, CreateWithSetup) — templates are create-time-only —
// with the create's transaction as pool and q.
func ApplyTemplate(ctx context.Context, pool database.TxBeginner, q db.Querier, tenantID, projectID, templateID int64) (db.Project, db.SettingsProfile, error) {
	var zero db.Project
	var zeroProfile db.SettingsProfile
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: projectID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, zeroProfile, validationf("project not found")
		}
		return zero, zeroProfile, fmt.Errorf("get project: %w", err)
	}
	tmpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: templateID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, zeroProfile, validationf("template not found in this tenant")
		}
		return zero, zeroProfile, fmt.Errorf("get template: %w", err)
	}
	tmplProfile, err := q.GetSettingsProfile(ctx, tmpl.SettingsProfileID)
	if err != nil {
		return zero, zeroProfile, fmt.Errorf("get template settings profile: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, zeroProfile, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	updatedProfile, err := txq.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:             project.SettingsProfileID,
		FuzzyThreshold: tmplProfile.FuzzyThreshold,
		MtOnUpload:     tmplProfile.MtOnUpload,
		TmOnUpload:     tmplProfile.TmOnUpload,
		QaProfileID:    tmplProfile.QaProfileID,
		// Explicit, not nil-keeps-stored: applying a template must carry ITS
		// penalties, lookup order, auto-complete and inline-HTML choices, not leave the project's defaults
		// in place.
		SublanguagePenaltyPct: &tmplProfile.SublanguagePenaltyPct,
		NearExactPenaltyPct:   &tmplProfile.NearExactPenaltyPct,
		AutoComplete:          &tmplProfile.AutoComplete,
		InlineHtml:            &tmplProfile.InlineHtml,
		TmLookupOrder:         &tmplProfile.TmLookupOrder,
	})
	if err != nil {
		return zero, zeroProfile, fmt.Errorf("copy template settings: %w", err)
	}
	if tmplProfile.TmProfileID != nil {
		updatedProfile, err = txq.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{
			ID: project.SettingsProfileID, TmProfileID: tmplProfile.TmProfileID,
		})
		if err != nil {
			return zero, zeroProfile, fmt.Errorf("copy template TM profile link: %w", err)
		}
	}
	// Stamp provenance. In the create-time-only model template_id is the only
	// record of where a project's settings came from, so a failed stamp aborts
	// the apply (rolls back with the tx) — "applied but unstamped" would leave
	// the Settings tab lying.
	project, err = txq.SetProjectTemplate(ctx, db.SetProjectTemplateParams{
		ID: projectID, TemplateID: &templateID, TenantID: tenantID,
	})
	if err != nil {
		return zero, zeroProfile, fmt.Errorf("stamp template provenance: %w", err)
	}
	if err := settings.CopyAssignments(ctx, txq, tmpl.SettingsProfileID, project.SettingsProfileID); err != nil {
		return zero, zeroProfile, fmt.Errorf("copy assignments: %w", err)
	}
	// No audit event here: ApplyTemplate is create-time-only, so the template
	// is folded into the caller's single project.created event.
	// TypeTemplateApplied stays defined because stored events of that type
	// still render in the FE.
	if err := tx.Commit(ctx); err != nil {
		return zero, zeroProfile, fmt.Errorf("commit: %w", err)
	}
	return project, updatedProfile, nil
}
