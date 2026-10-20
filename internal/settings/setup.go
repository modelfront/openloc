package settings

// setup.go — the optional initial configuration a client sends when creating
// a project or a template: scalar settings, the four assignment lists, and a
// workflow (library link and/or explicit steps). Both create endpoints decode
// the same shape and apply it to a fresh settings profile through ApplySetup,
// so the New project page can save its form either as a project or as a
// template with one payload.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// Setup is applied right after creation. Nil slices mean "leave as created"
// (template values or defaults); non-nil slices are full replaces, matching
// the PUT endpoints' semantics.
type Setup struct {
	FuzzyThreshold *float32 `json:"fuzzy_threshold"`
	MtOnUpload     *bool    `json:"mt_on_upload"`
	TmOnUpload     *bool    `json:"tm_on_upload"`
	// nil keeps the stored value (on by default, or
	// whatever the applied template carries).
	AutoComplete *bool `json:"auto_complete"`
	// InlineHTML: strings of JSON/PO/TXT/XLSX files are HTML; nil keeps stored.
	InlineHTML *bool `json:"inline_html"`
	// TM match penalties; nil keeps the stored value.
	SublanguagePenaltyPct *int32 `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   *int32 `json:"near_exact_penalty_pct"`
	// TM lookup order; nil keeps the stored value.
	TMLookupOrder *string `json:"tm_lookup_order"`
	// QA profile is tri-state like PATCH /settings: QaProfileID set
	// = use it; ClearQAProfile = explicit null/0 in the request, meaning "no
	// QA profile" — needed so clearing a template's prefilled profile in the
	// wizard actually clears it; both zero = keep whatever creation left.
	QaProfileID    *int64 `json:"-"`
	ClearQAProfile bool   `json:"-"`
	// Link to a TM profile. Applied after the template so an explicit pick
	// wins; contradicts TMAssignments (rejected by ValidateSetup).
	TMProfileID   *int64              `json:"-"`
	TMAssignments []TMAssignmentInput `json:"tm_assignments"`
	// MTRouterID points the profile at an MT router (nil = leave as created).
	MTRouterID      *int64                `json:"mt_router_id"`
	TermAssignments []TermAssignmentInput `json:"term_assignments"`
	// WorkflowID copies a library workflow onto the profile (after the
	// template, so it overrides the template's steps). WorkflowSteps, when
	// present, is applied last — the wizard's per-step tweaks on top.
	WorkflowID    *int64      `json:"workflow_id"`
	WorkflowSteps []StepInput `json:"workflow_steps"`
}

// HasSettings reports whether any scalar is set (an UpdateSettingsProfile is needed).
func (c Setup) HasSettings() bool {
	return c.FuzzyThreshold != nil || c.MtOnUpload != nil || c.TmOnUpload != nil ||
		c.QaProfileID != nil || c.ClearQAProfile || c.AutoComplete != nil || c.InlineHTML != nil ||
		c.SublanguagePenaltyPct != nil || c.NearExactPenaltyPct != nil ||
		c.TMLookupOrder != nil
}

// HasWorkflow reports whether the setup decides the workflow steps itself.
func (c Setup) HasWorkflow() bool { return c.WorkflowID != nil || len(c.WorkflowSteps) > 0 }

// ValidateSetup runs every caller-fixable check before any row exists; the
// errors are httputil client errors. TM/term tenant ownership is enforced in
// Apply{TM,Term}Assignments, shared with the PUT endpoints.
func ValidateSetup(ctx context.Context, q db.Querier, tenantID int64, cfg Setup) error {
	if err := ValidateTMLookupOrder(cfg.TMLookupOrder); err != nil {
		return err
	}
	if cfg.TMProfileID != nil {
		if cfg.TMAssignments != nil {
			return httputil.NewClientError("tm_profile_id and tm_assignments cannot both be set; a linked project takes its TMs from the profile")
		}
		if _, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: *cfg.TMProfileID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httputil.NewClientError("TM profile not found in this tenant")
			}
			return fmt.Errorf("verify TM profile: %w", err)
		}
	}
	if cfg.QaProfileID != nil {
		if err := VerifyQAProfile(ctx, q, tenantID, *cfg.QaProfileID); err != nil {
			return err
		}
	}
	if cfg.MTRouterID != nil {
		if _, err := q.GetMTRouter(ctx, db.GetMTRouterParams{ID: *cfg.MTRouterID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httputil.NewClientError("MT router not found in this tenant")
			}
			return fmt.Errorf("verify MT router: %w", err)
		}
	}
	if cfg.WorkflowID != nil {
		if _, err := q.GetApplicableWorkflow(ctx, db.GetApplicableWorkflowParams{ID: *cfg.WorkflowID, TenantID: tenantID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httputil.NewClientError("workflow not found in this tenant")
			}
			return fmt.Errorf("verify workflow: %w", err)
		}
	}
	// Steps resolve against the library; at create time the profile has no
	// rows yet, so existing_step_id has nothing to point at.
	if len(cfg.WorkflowSteps) > 0 {
		if err := ValidateStepInputs(ctx, q, tenantID, nil, cfg.WorkflowSteps); err != nil {
			return err
		}
	}
	return nil
}

// ApplySetup writes the setup onto an existing settings profile: scalars,
// TM list or TM profile link, MT router, term list, then the workflow
// (library copy first, explicit steps on top). Each assignment replace is individually
// transactional via the shared helpers.
func ApplySetup(ctx context.Context, pool database.TxBeginner, q db.Querier, tenantID, profileID int64, cfg Setup) error {
	if cfg.HasSettings() {
		profile, err := q.GetSettingsProfile(ctx, profileID)
		if err != nil {
			return fmt.Errorf("get settings profile: %w", err)
		}
		threshold := profile.FuzzyThreshold
		if cfg.FuzzyThreshold != nil {
			threshold = *cfg.FuzzyThreshold
		}
		mtOnUpload := profile.MtOnUpload
		if cfg.MtOnUpload != nil {
			mtOnUpload = *cfg.MtOnUpload
		}
		// A setup that also sets the workflow is checked by the step write
		// below, which switches MT on for a Pre-translation workflow.
		if !cfg.HasWorkflow() {
			if err := CheckPretranslateMT(ctx, q, profileID, mtOnUpload); err != nil {
				return err
			}
		}
		tmOnUpload := profile.TmOnUpload
		if cfg.TmOnUpload != nil {
			tmOnUpload = *cfg.TmOnUpload
		}
		qaProfileID := profile.QaProfileID
		if cfg.ClearQAProfile {
			qaProfileID = nil
		} else if cfg.QaProfileID != nil {
			qaProfileID = cfg.QaProfileID
		}
		if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
			ID:             profileID,
			FuzzyThreshold: threshold,
			MtOnUpload:     mtOnUpload,
			TmOnUpload:     tmOnUpload,
			QaProfileID:    qaProfileID,
			// nil-keeps-stored, so an absent field leaves the template's (or
			// column's) value rather than silently switching it off.
			AutoComplete:          cfg.AutoComplete,
			InlineHtml:            cfg.InlineHTML,
			SublanguagePenaltyPct: ClampPctPtr(cfg.SublanguagePenaltyPct),
			NearExactPenaltyPct:   ClampPctPtr(cfg.NearExactPenaltyPct),
			TmLookupOrder:         cfg.TMLookupOrder,
		}); err != nil {
			return fmt.Errorf("apply settings: %w", err)
		}
	}

	if cfg.TMAssignments != nil {
		if _, err := ApplyTMAssignmentsTx(ctx, pool, tenantID, profileID, cfg.TMAssignments); err != nil {
			return fmt.Errorf("apply TM assignments: %w", err)
		}
	}
	if cfg.TMProfileID != nil {
		if _, err := LinkTMProfileTx(ctx, pool, tenantID, profileID, *cfg.TMProfileID); err != nil {
			return fmt.Errorf("link TM profile: %w", err)
		}
	}
	if cfg.MTRouterID != nil {
		if err := q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{ID: profileID, MtRouterID: cfg.MTRouterID}); err != nil {
			return fmt.Errorf("apply MT router: %w", err)
		}
	}
	if cfg.TermAssignments != nil {
		if _, err := ApplyTermAssignmentsTx(ctx, pool, tenantID, profileID, cfg.TermAssignments); err != nil {
			return fmt.Errorf("apply terminology assignments: %w", err)
		}
	}
	if cfg.WorkflowID != nil {
		if err := applyWorkflowTx(ctx, pool, tenantID, *cfg.WorkflowID, profileID); err != nil {
			return fmt.Errorf("apply workflow: %w", err)
		}
	}
	if len(cfg.WorkflowSteps) > 0 {
		if _, err := ReplaceWorkflowStepsTx(ctx, pool, tenantID, profileID, cfg.WorkflowSteps); err != nil {
			return fmt.Errorf("apply workflow steps: %w", err)
		}
	}
	return nil
}

// applyWorkflowTx runs ApplyWorkflow in its own transaction.
func applyWorkflowTx(ctx context.Context, pool database.TxBeginner, tenantID, workflowID, profileID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := ApplyWorkflow(ctx, db.New(tx), tenantID, workflowID, profileID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifyQAProfile refuses a QA profile id that is not one of tenantID's, with
// the same client error whether it belongs to another tenant or to none.
func VerifyQAProfile(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := q.GetQAProfile(ctx, db.GetQAProfileParams{ID: id, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httputil.NewClientError("QA profile not found in this tenant")
		}
		return fmt.Errorf("verify QA profile: %w", err)
	}
	return nil
}
