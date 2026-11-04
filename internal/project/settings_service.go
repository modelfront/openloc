package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/mtrouter"
	"openloc/internal/projectevents"
	"openloc/internal/segmentationapi"
	"openloc/internal/settings"
)

// The settings services behind /api/projects/{id}/settings, /tms and
// /terminology, shared with the MCP project tools. Caller mistakes come back
// as httputil.ClientError, an edit refused because the project follows a TM
// profile as settings.ErrTMProfileLinked, an unknown or foreign project as
// pgx.ErrNoRows.

// SettingsPatch is a partial update of a project's settings profile. A nil
// field keeps its current value.
type SettingsPatch struct {
	FuzzyThreshold        *float32
	MtOnUpload            *bool
	TmOnUpload            *bool
	SublanguagePenaltyPct *int32
	NearExactPenaltyPct   *int32
	AutoComplete          *bool
	LinguistTerms         *string
	TMLookupOrder         *string
	// QAProfileID, MTRouterID: 0 clears, a positive id sets.
	QAProfileID *int64
	MTRouterID  *int64
	// TMProfileID links the project to a TM profile. Leaving one is
	// DetachTMProfile's job, so there is no clear.
	TMProfileID *int64
	// SegmentationProfileID picks the profile new uploads are split with; it
	// must fit the project's source language. There is no clear.
	SegmentationProfileID *int64
	// FilterProfileIDs picks the filter profile per format: a nil id goes
	// back to the format's Default, a format left out keeps its choice.
	FilterProfileIDs map[string]*int64
}

// SettingsDetail is a project's settings with every assignment list — what
// GET /settings returns. TM rows and match scoring are the effective ones:
// the linked TM profile's when the project follows one.
type SettingsDetail struct {
	Project         ProjectSettings                    `json:"project"`
	TMAssignments   []db.SettingsTmAssignment          `json:"tm_assignments"`
	TMProfile       *settings.TMProfileRef             `json:"tm_profile"`
	TermAssignments []db.SettingsTerminologyAssignment `json:"term_assignments"`
	WorkflowSteps   []settings.StepWithDeadlines       `json:"workflow_steps"`
}

// Settings loads a project's settings and assignment lists.
func (h *Handler) Settings(ctx context.Context, tenantID, id int64) (SettingsDetail, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return SettingsDetail{}, err
	}
	profile, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return SettingsDetail{}, fmt.Errorf("get settings profile: %w", err)
	}
	scope, err := settings.ResolveTMScope(ctx, h.queries, tenantID, profile)
	if err != nil {
		return SettingsDetail{}, err
	}
	terms, err := h.queries.ListSettingsTermAssignments(ctx, project.SettingsProfileID)
	if err != nil {
		return SettingsDetail{}, fmt.Errorf("list terminology assignments: %w", err)
	}
	steps, err := settings.ListStepsWithDeadlines(ctx, h.queries, tenantID, project.SettingsProfileID)
	if err != nil {
		return SettingsDetail{}, err
	}
	ps, err := h.newProjectSettings(ctx, tenantID, project, scope.Effective(profile))
	if err != nil {
		return SettingsDetail{}, err
	}
	return SettingsDetail{
		Project:         ps,
		TMAssignments:   scope.TMAssignments,
		TMProfile:       scope.Linked,
		TermAssignments: terms,
		WorkflowSteps:   steps,
	}, nil
}

// UpdateSettings applies the patch to the project's settings profile and
// records a project.settings_updated event when anything changed.
func (h *Handler) UpdateSettings(ctx context.Context, tenantID, id int64, p SettingsPatch) (ProjectSettings, error) {
	if p.QAProfileID != nil && *p.QAProfileID < 0 {
		return ProjectSettings{}, httputil.NewClientError("invalid qa_profile_id (must be null, 0, or positive integer)")
	}
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return ProjectSettings{}, err
	}
	profile, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return ProjectSettings{}, fmt.Errorf("get settings profile: %w", err)
	}

	var mtRouter *int64
	if p.MTRouterID != nil && *p.MTRouterID > 0 {
		mtRouter = p.MTRouterID
	}
	if p.MTRouterID != nil {
		if msg := mtrouter.ValidateProfileRouter(ctx, h.queries, tenantID, mtRouter); msg != "" {
			return ProjectSettings{}, httputil.NewClientError(msg)
		}
	}
	qaProfileID := profile.QaProfileID
	if p.QAProfileID != nil {
		qaProfileID = nil
		if n := *p.QAProfileID; n > 0 {
			if err := settings.VerifyQAProfile(ctx, h.queries, tenantID, n); err != nil {
				return ProjectSettings{}, err
			}
			qaProfileID = &n
		}
	}
	if err := settings.ValidateTMLookupOrder(p.TMLookupOrder); err != nil {
		return ProjectSettings{}, err
	}
	if err := settings.ValidateLinguistTerms(p.LinguistTerms); err != nil {
		return ProjectSettings{}, err
	}
	if p.SegmentationProfileID != nil {
		if err := segmentationapi.Check(ctx, h.queries, tenantID, *p.SegmentationProfileID, project.SourceLanguage); err != nil {
			return ProjectSettings{}, err
		}
	}
	if err := filterprofile.ValidateChoices(ctx, h.queries, tenantID, p.FilterProfileIDs); err != nil {
		return ProjectSettings{}, err
	}
	linking := p.TMProfileID != nil
	// Match scoring belongs to the profile while linked.
	if err := settings.GuardTMScoringEdit(profile.TmProfileID != nil || linking, p.FuzzyThreshold, p.SublanguagePenaltyPct, p.NearExactPenaltyPct, p.TMLookupOrder); err != nil {
		return ProjectSettings{}, err
	}

	if err := settings.CheckPretranslateMT(ctx, h.queries, project.SettingsProfileID, deref(p.MtOnUpload, profile.MtOnUpload)); err != nil {
		return ProjectSettings{}, err
	}
	if p.MTRouterID != nil && project.Status != db.ProjectStatusDraft {
		if err := settings.CheckPretranslateRouter(ctx, h.queries, project.SettingsProfileID, mtRouter); err != nil {
			return ProjectSettings{}, err
		}
	}
	// One transaction: a refused pick (a profile deleted since the checks
	// above) saves nothing, and no event is written for half a patch.
	var updated db.SettingsProfile
	var filtersBefore, filtersAfter []filterprofile.Choice
	err = database.InTx(ctx, h.pool, func(tx pgx.Tx, q db.Querier) error {
		var err error
		updated, err = q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
			ID:                    project.SettingsProfileID,
			FuzzyThreshold:        deref(p.FuzzyThreshold, profile.FuzzyThreshold),
			MtOnUpload:            deref(p.MtOnUpload, profile.MtOnUpload),
			TmOnUpload:            deref(p.TmOnUpload, profile.TmOnUpload),
			QaProfileID:           qaProfileID,
			SublanguagePenaltyPct: settings.ClampPctPtr(p.SublanguagePenaltyPct),
			NearExactPenaltyPct:   settings.ClampPctPtr(p.NearExactPenaltyPct),
			AutoComplete:          p.AutoComplete,
			LinguistTerms:         p.LinguistTerms,
			TmLookupOrder:         p.TMLookupOrder,
		})
		if err != nil {
			return fmt.Errorf("update settings profile: %w", err)
		}
		if p.MTRouterID != nil {
			if err := q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{ID: project.SettingsProfileID, MtRouterID: mtRouter}); err != nil {
				return fmt.Errorf("set MT router: %w", err)
			}
			updated.MtRouterID = mtRouter
		}
		if linking {
			// A savepoint inside this transaction.
			if updated, err = settings.LinkTMProfileTx(ctx, tx, tenantID, project.SettingsProfileID, *p.TMProfileID); err != nil {
				return err
			}
		}
		if p.SegmentationProfileID != nil {
			updated, err = q.SetSettingsProfileSegmentationProfile(ctx, db.SetSettingsProfileSegmentationProfileParams{
				ID: project.SettingsProfileID, SegmentationProfileID: p.SegmentationProfileID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return segmentationapi.ErrProfileNotFound
			}
			if err != nil {
				return fmt.Errorf("set segmentation profile: %w", err)
			}
		}
		if filtersBefore, err = filterprofile.Choices(ctx, q, project.SettingsProfileID); err != nil {
			return err
		}
		if err := filterprofile.SetChoices(ctx, q, tenantID, project.SettingsProfileID, p.FilterProfileIDs); err != nil {
			return err
		}
		filtersAfter, err = filterprofile.Choices(ctx, q, project.SettingsProfileID)
		return err
	})
	if err != nil {
		return ProjectSettings{}, err
	}

	changes := projectevents.Diff(settingsMap(profile), settingsMap(updated))
	from, to := filterNames(filtersBefore), filterNames(filtersAfter)
	for _, format := range filterprofile.Formats {
		if from[format] != to[format] {
			if changes == nil {
				changes = map[string]projectevents.Change{}
			}
			changes["filter_profile_"+format] = projectevents.Change{From: from[format], To: to[format]}
		}
	}
	if changes != nil {
		if ch, ok := changes["qa_profile_id"]; ok {
			changes["qa_profile_name"] = projectevents.Change{
				From: qaProfileName(ctx, h.queries, tenantID, idFromChange(ch.From)),
				To:   qaProfileName(ctx, h.queries, tenantID, idFromChange(ch.To)),
			}
		}
		if ch, ok := changes["segmentation_profile_id"]; ok {
			changes["segmentation_profile_name"] = projectevents.Change{
				From: segmentationProfileName(ctx, h.queries, tenantID, idFromChange(ch.From)),
				To:   segmentationProfileName(ctx, h.queries, tenantID, idFromChange(ch.To)),
			}
		}
		if ch, ok := changes["mt_router_id"]; ok {
			changes["mt_router_name"] = projectevents.Change{
				From: mtRouterName(ctx, h.queries, tenantID, idFromChange(ch.From)),
				To:   mtRouterName(ctx, h.queries, tenantID, idFromChange(ch.To)),
			}
		}
		actorID, actorName := projectevents.ActorFromContext(ctx)
		projectevents.Emit(ctx, h.queries, projectevents.Event{
			ProjectID: project.ID,
			TenantID:  tenantID,
			Type:      projectevents.TypeSettingsUpdated,
			ActorID:   actorID,
			ActorName: actorName,
			Details:   map[string]any{"changes": changes},
		})
	}

	// Threshold + penalties come from the linked TM profile when there is one.
	scope, err := settings.ResolveTMScope(ctx, h.queries, tenantID, updated)
	if err != nil {
		return ProjectSettings{}, err
	}
	return h.newProjectSettings(ctx, tenantID, project, scope.Effective(updated))
}

// SetTMAssignments replaces the project's TM list in one transaction. Refused
// with settings.ErrTMProfileLinked while the project follows a TM profile.
func (h *Handler) SetTMAssignments(ctx context.Context, tenantID, id int64, in []settings.TMAssignmentInput) ([]db.SettingsTmAssignment, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	profile, err := h.queries.GetSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		return nil, fmt.Errorf("get settings profile: %w", err)
	}
	if profile.TmProfileID != nil {
		return nil, settings.ErrTMProfileLinked
	}
	// Audit pre-read: a failed read skips the event but never blocks the save.
	before, beforeErr := h.queries.ListSettingsTMAssignments(ctx, project.SettingsProfileID)
	if beforeErr != nil {
		logging.Warn(ctx, beforeErr, "audit pre-read failed; tm_assignments_set event skipped", "project-event-preread-failed", logging.ProjectID(project.ID))
	}
	after, err := settings.ApplyTMAssignmentsTx(ctx, h.pool, tenantID, project.SettingsProfileID, in)
	if err != nil {
		return nil, err
	}
	if beforeErr == nil {
		emitAssignmentsSet(ctx, h.queries, projectevents.TypeTMAssignmentsSet, project.ID, tenantID,
			tmAssignmentsPayload(ctx, h.queries, tenantID, before),
			tmAssignmentsPayload(ctx, h.queries, tenantID, after))
	}
	return after, nil
}

// SetTermAssignments replaces the project's term base list in one transaction.
func (h *Handler) SetTermAssignments(ctx context.Context, tenantID, id int64, in []settings.TermAssignmentInput) ([]db.SettingsTerminologyAssignment, error) {
	project, err := h.queries.GetProject(ctx, db.GetProjectParams{ID: id, TenantID: tenantID})
	if err != nil {
		return nil, err
	}
	before, beforeErr := h.queries.ListSettingsTermAssignments(ctx, project.SettingsProfileID)
	if beforeErr != nil {
		logging.Warn(ctx, beforeErr, "audit pre-read failed; terminology_assignments_set event skipped", "project-event-preread-failed", logging.ProjectID(project.ID))
	}
	after, err := settings.ApplyTermAssignmentsTx(ctx, h.pool, tenantID, project.SettingsProfileID, in)
	if err != nil {
		return nil, err
	}
	if beforeErr == nil {
		emitAssignmentsSet(ctx, h.queries, projectevents.TypeTermAssignmentsSet, project.ID, tenantID,
			termAssignmentsPayload(ctx, h.queries, tenantID, before),
			termAssignmentsPayload(ctx, h.queries, tenantID, after))
	}
	return after, nil
}

// deref returns *p, or fallback when p is nil.
func deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// filterNames is a project's filter profile name per format, for the
// settings event; a format without a pick is its Default.
func filterNames(choices []filterprofile.Choice) map[string]string {
	out := make(map[string]string, len(filterprofile.Formats))
	for _, f := range filterprofile.Formats {
		out[f] = "Default"
	}
	for _, c := range choices {
		out[c.Format] = c.Name
	}
	return out
}
