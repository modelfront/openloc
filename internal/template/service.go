package template

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/mtrouter"
	"openloc/internal/settings"
)

// Transport-free template writes, shared by the REST handler and the MCP
// tools. Caller mistakes come back as httputil.ClientError, an edit to TM
// scoring on a template that follows a TM profile as settings.ErrTMProfileLinked,
// and an id outside the tenant as pgx.ErrNoRows.

// CreateInput is a new template: its name, what a project made from it starts
// with, and the initial configuration POST /api/projects also accepts.
type CreateInput struct {
	Name string
	// nil = on / 0.75, the defaults a new settings profile gets.
	MtOnUpload     *bool
	TmOnUpload     *bool
	FuzzyThreshold *float32
	// Project name, languages, customer and domain a project made from the
	// template starts with.
	ProjectName     *string
	SourceLanguage  *string
	TargetLanguages []string
	CustomerID      *int64
	Domain          *string
	Setup           settings.Setup
}

// Create validates and inserts a template with its own settings profile, in
// one transaction: a setup refused at any step leaves nothing behind. The
// workflow steps are the setup's (library workflow and/or steps), else the
// default pair.
func Create(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID int64, in CreateInput) (db.Template, db.SettingsProfile, error) {
	customerID, err := customerInTenant(ctx, q, tenantID, in.CustomerID)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	sourceLang, targetLangs, err := normalizeLanguages(in.SourceLanguage, in.TargetLanguages)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, httputil.NewClientError(err.Error())
	}
	if err := settings.ValidateSetup(ctx, q, tenantID, in.Setup); err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return db.Template{}, db.SettingsProfile{}, httputil.NewClientError("name is required")
	}
	threshold := float32(0.75)
	if in.FuzzyThreshold != nil {
		threshold = *in.FuzzyThreshold
	}
	if threshold < 0 || threshold > 1 {
		return db.Template{}, db.SettingsProfile{}, httputil.NewClientError("fuzzy_threshold must be between 0.0 and 1.0")
	}
	mtOnUpload := true
	if in.MtOnUpload != nil {
		mtOnUpload = *in.MtOnUpload
	}
	tmOnUpload := true
	if in.TmOnUpload != nil {
		tmOnUpload = *in.TmOnUpload
	}

	var tmpl db.Template
	var profile db.SettingsProfile
	err = database.InTx(ctx, pool, func(tx pgx.Tx, q db.Querier) error {
		var err error
		profile, err = q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
			TenantID:       tenantID,
			FuzzyThreshold: threshold,
			MtOnUpload:     mtOnUpload,
			TmOnUpload:     tmOnUpload,
		})
		if err != nil {
			return fmt.Errorf("create settings profile: %w", err)
		}
		if err := settings.ApplySetup(ctx, tx, q, tenantID, profile.ID, in.Setup); err != nil {
			return fmt.Errorf("apply template setup: %w", err)
		}
		if !in.Setup.HasWorkflow() {
			if err := settings.CreateDefaultWorkflowSteps(ctx, q, tenantID, profile.ID); err != nil {
				return fmt.Errorf("create default workflow steps: %w", err)
			}
		}
		// Re-read: the setup may have changed scalars and the provenance pointer.
		if profile, err = q.GetSettingsProfile(ctx, profile.ID); err != nil {
			return fmt.Errorf("get settings profile: %w", err)
		}
		tmpl, err = q.CreateTemplate(ctx, db.CreateTemplateParams{
			Name:              name,
			TenantID:          tenantID,
			SettingsProfileID: profile.ID,
			ProjectName:       trimText(in.ProjectName),
			SourceLanguage:    sourceLang,
			TargetLanguages:   targetLangs,
			CustomerID:        customerID,
			Domain:            trimDomain(in.Domain),
		})
		if err != nil {
			return fmt.Errorf("create template: %w", err)
		}
		return nil
	})
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	return tmpl, profile, nil
}

// NullableID is a tri-state id field: Set false keeps the stored value; Set
// true with a nil ID clears it; Set true with an ID sets it.
type NullableID struct {
	Set bool
	ID  *int64
}

// UpdateInput is a partial edit: every nil (or unset) field keeps the stored
// value.
type UpdateInput struct {
	Name                  *string
	MtOnUpload            *bool
	TmOnUpload            *bool
	AutoComplete          *bool
	InlineHTML            *bool
	FuzzyThreshold        *float32
	SublanguagePenaltyPct *int32
	NearExactPenaltyPct   *int32
	TMLookupOrder         *string
	QAProfile             NullableID
	MTRouter              NullableID
	// LinkTMProfile links the template to a TM profile. Leaving a profile goes
	// through the detach endpoint, which copies its rows.
	LinkTMProfile *int64
	Customer      NullableID
	// A present value is trimmed, and empty clears.
	ProjectName *string
	Domain      *string
	// A present value replaces (empty source / empty list clear).
	SourceLanguage  *string
	TargetLanguages []string
}

// UpdatePlan is a validated edit: the template as it is now and the rows the
// edit will write.
type UpdatePlan struct {
	Template db.Template
	profile  db.UpdateSettingsProfileParams
	template db.UpdateTemplateParams
	in       UpdateInput
}

// PlanUpdate checks an edit exactly as Update does, without writing it.
func PlanUpdate(ctx context.Context, q db.Querier, tenantID, id int64, in UpdateInput) (UpdatePlan, error) {
	fail := func(msg string) (UpdatePlan, error) { return UpdatePlan{}, httputil.NewClientError(msg) }
	existing, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: id, TenantID: tenantID})
	if err != nil {
		return UpdatePlan{}, err
	}
	customerID := existing.CustomerID
	if in.Customer.Set {
		if customerID, err = customerInTenant(ctx, q, tenantID, in.Customer.ID); err != nil {
			return UpdatePlan{}, err
		}
	}
	domain := existing.Domain
	if in.Domain != nil {
		domain = trimDomain(in.Domain)
	}
	projectName := existing.ProjectName
	if in.ProjectName != nil {
		projectName = trimText(in.ProjectName)
	}
	sourceLang, targetLangs := existing.SourceLanguage, existing.TargetLanguages
	if in.SourceLanguage != nil || in.TargetLanguages != nil {
		nextSource := existing.SourceLanguage
		if in.SourceLanguage != nil {
			nextSource = in.SourceLanguage
		}
		nextTargets := existing.TargetLanguages
		if in.TargetLanguages != nil {
			nextTargets = in.TargetLanguages
		}
		if sourceLang, targetLangs, err = normalizeLanguages(nextSource, nextTargets); err != nil {
			return fail(err.Error())
		}
	}

	profile, err := q.GetSettingsProfile(ctx, existing.SettingsProfileID)
	if err != nil {
		return UpdatePlan{}, fmt.Errorf("get settings profile: %w", err)
	}
	if err := settings.ValidateTMLookupOrder(in.TMLookupOrder); err != nil {
		return UpdatePlan{}, err
	}
	linking := in.LinkTMProfile != nil
	if err := settings.GuardTMScoringEdit(profile.TmProfileID != nil || linking, in.FuzzyThreshold, in.SublanguagePenaltyPct, in.NearExactPenaltyPct, in.TMLookupOrder); err != nil {
		return UpdatePlan{}, err
	}
	if linking {
		if _, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: *in.LinkTMProfile, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fail("TM profile not found in this tenant")
			}
			return UpdatePlan{}, fmt.Errorf("verify TM profile: %w", err)
		}
	}

	name := existing.Name
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if trimmed == "" {
			return fail("name must not be empty")
		}
		name = trimmed
	}
	threshold := profile.FuzzyThreshold
	if in.FuzzyThreshold != nil {
		threshold = *in.FuzzyThreshold
	}
	if threshold < 0 || threshold > 1 {
		return fail("fuzzy_threshold must be between 0.0 and 1.0")
	}
	mtOnUpload := profile.MtOnUpload
	if in.MtOnUpload != nil {
		mtOnUpload = *in.MtOnUpload
	}
	if err := settings.CheckPretranslateMT(ctx, q, existing.SettingsProfileID, mtOnUpload); err != nil {
		return UpdatePlan{}, err
	}
	tmOnUpload := profile.TmOnUpload
	if in.TmOnUpload != nil {
		tmOnUpload = *in.TmOnUpload
	}
	qaProfileID := profile.QaProfileID
	if in.QAProfile.Set {
		if in.QAProfile.ID != nil {
			if err := settings.VerifyQAProfile(ctx, q, tenantID, *in.QAProfile.ID); err != nil {
				return UpdatePlan{}, err
			}
		}
		qaProfileID = in.QAProfile.ID
	}
	if in.MTRouter.Set {
		if msg := mtrouter.ValidateProfileRouter(ctx, q, tenantID, in.MTRouter.ID); msg != "" {
			return fail(msg)
		}
	}
	return UpdatePlan{
		Template: existing,
		in:       in,
		profile: db.UpdateSettingsProfileParams{
			ID:                    existing.SettingsProfileID,
			FuzzyThreshold:        threshold,
			MtOnUpload:            mtOnUpload,
			TmOnUpload:            tmOnUpload,
			QaProfileID:           qaProfileID,
			SublanguagePenaltyPct: settings.ClampPctPtr(in.SublanguagePenaltyPct),
			NearExactPenaltyPct:   settings.ClampPctPtr(in.NearExactPenaltyPct),
			AutoComplete:          in.AutoComplete,
			InlineHtml:            in.InlineHTML,
			TmLookupOrder:         in.TMLookupOrder,
		},
		template: db.UpdateTemplateParams{
			ID:              id,
			Name:            name,
			TenantID:        tenantID,
			ProjectName:     projectName,
			SourceLanguage:  sourceLang,
			TargetLanguages: targetLangs,
			CustomerID:      customerID,
			Domain:          domain,
		},
	}, nil
}

// Update applies a partial edit to a template and its settings profile. It
// never touches projects: a project copies its template's configuration at
// creation.
func Update(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64, in UpdateInput) (db.Template, db.SettingsProfile, error) {
	plan, err := PlanUpdate(ctx, q, tenantID, id, in)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	updated, err := q.UpdateSettingsProfile(ctx, plan.profile)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("update settings profile: %w", err)
	}
	profileID := plan.Template.SettingsProfileID
	if in.MTRouter.Set {
		if err := q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{ID: profileID, MtRouterID: in.MTRouter.ID}); err != nil {
			return db.Template{}, db.SettingsProfile{}, fmt.Errorf("set MT router: %w", err)
		}
		updated.MtRouterID = in.MTRouter.ID
	}
	if in.LinkTMProfile != nil {
		if updated, err = settings.LinkTMProfileTx(ctx, pool, tenantID, profileID, *in.LinkTMProfile); err != nil {
			return db.Template{}, db.SettingsProfile{}, err
		}
	}
	tmpl, err := q.UpdateTemplate(ctx, plan.template)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	return tmpl, updated, nil
}

// Duplicate clones a template: a new settings profile with the same scalars,
// a "Copy of {name}" template, and all four assignment lists, in one
// transaction. Templates have no unique-name constraint, so the copy's name
// may repeat.
func Duplicate(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64) (db.Template, db.SettingsProfile, error) {
	src, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, err
	}
	srcProfile, err := q.GetSettingsProfile(ctx, src.SettingsProfileID)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("get settings profile: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	// Scalars, penalties, auto-complete and the TM profile link travel
	// together; a copy that drifted on any one would match differently.
	newProfile, err := settings.CreateProfileLike(ctx, txq, tenantID, srcProfile)
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("create settings profile: %w", err)
	}
	dup, err := txq.CreateTemplate(ctx, db.CreateTemplateParams{
		Name:              "Copy of " + src.Name,
		TenantID:          tenantID,
		SettingsProfileID: newProfile.ID,
		ProjectName:       src.ProjectName,
		SourceLanguage:    src.SourceLanguage,
		TargetLanguages:   src.TargetLanguages,
		CustomerID:        src.CustomerID,
		Domain:            src.Domain,
	})
	if err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("create template: %w", err)
	}
	if err := settings.CopyAssignments(ctx, txq, src.SettingsProfileID, newProfile.ID); err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("copy assignments: %w", err)
	}
	// CopyAssignments writes onto the profile row (the MT router pointer), so
	// the result comes from a fresh read.
	if newProfile, err = txq.GetSettingsProfile(ctx, newProfile.ID); err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("reload settings profile: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Template{}, db.SettingsProfile{}, fmt.Errorf("commit: %w", err)
	}
	return dup, newProfile, nil
}

// ProjectsFrom counts the projects created from a template. Callers verify
// the template's tenant first.
func ProjectsFrom(ctx context.Context, q db.Querier, tenantID, id int64) (int64, error) {
	n, err := q.CountProjectsFromTemplate(ctx, db.CountProjectsFromTemplateParams{TemplateID: &id, TenantID: tenantID})
	if err != nil {
		return 0, fmt.Errorf("count projects from template: %w", err)
	}
	return n, nil
}

// Delete removes a template and its settings profile in one transaction.
// Projects made from it keep their own copied configuration.
func Delete(ctx context.Context, q db.Querier, pool *pgxpool.Pool, tenantID, id int64) error {
	tmpl, err := q.GetTemplate(ctx, db.GetTemplateParams{ID: id, TenantID: tenantID})
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)
	if err := txq.DeleteTemplate(ctx, db.DeleteTemplateParams{ID: id, TenantID: tenantID}); err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	if err := txq.DeleteSettingsProfile(ctx, tmpl.SettingsProfileID); err != nil {
		return fmt.Errorf("delete settings profile: %w", err)
	}
	return tx.Commit(ctx)
}

// customerInTenant resolves an optional customer id: nil / 0 → no customer;
// otherwise the customer must belong to the tenant.
func customerInTenant(ctx context.Context, q db.Querier, tenantID int64, id *int64) (*int64, error) {
	if id == nil || *id == 0 {
		return nil, nil
	}
	if *id < 0 {
		return nil, httputil.NewClientError("invalid customer_id")
	}
	if _, err := q.GetCustomer(ctx, db.GetCustomerParams{ID: *id, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httputil.NewClientError("customer not found in this tenant")
		}
		return nil, fmt.Errorf("verify customer: %w", err)
	}
	return id, nil
}
