// Package tmprofile is the HTTP handler + service for TM profiles: a named,
// tenant-wide bundle of translation memories (mode / priority / penalty per
// TM) plus the match-scoring scalars, which projects and templates link to
// live. The TM rows and scoring live on the profile's own settings_profiles
// row; this package owns the tm_profiles row and the CRUD around it.
//
// Access scope: managers (owner/admin/PM) list, create, edit and set TMs;
// deleting a profile is owner/admin only. Wired in api.go.
package tmprofile

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/settings"
)

// maxNameLen caps the profile name defensively (same as customers).
const maxNameLen = 200

// defaultFuzzyThreshold matches the settings_profiles column default.
const defaultFuzzyThreshold float32 = 0.75

// Scoring is the profile-owned match scoring. Nil fields keep the stored (or
// default) value.
type Scoring struct {
	FuzzyThreshold        *float32 `json:"fuzzy_threshold"`
	SublanguagePenaltyPct *int32   `json:"sublanguage_penalty_pct"`
	NearExactPenaltyPct   *int32   `json:"near_exact_penalty_pct"`
	TMLookupOrder         *string  `json:"tm_lookup_order"`
}

// validateName trims and bounds a profile name, returning a client error on
// an empty or oversized value.
func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", httputil.NewClientError("name is required")
	}
	if len(name) > maxNameLen {
		return "", httputil.NewClientError("name too long (max 200 characters)")
	}
	return name, nil
}

func validateScoring(s Scoring) error {
	if v := s.FuzzyThreshold; v != nil && (*v < 0 || *v > 1) {
		return httputil.NewClientError("fuzzy_threshold must be between 0.0 and 1.0")
	}
	return settings.ValidateTMLookupOrder(s.TMLookupOrder)
}

// applyScoring writes the scoring onto the profile's settings row. Threshold nil
// keeps the stored value; penalties and lookup order are nil-keeps-stored, and
// penalties are clamped to 0–100.
func applyScoring(ctx context.Context, q db.Querier, sp db.SettingsProfile, s Scoring) (db.SettingsProfile, error) {
	if err := validateScoring(s); err != nil {
		return db.SettingsProfile{}, err
	}
	threshold := sp.FuzzyThreshold
	if s.FuzzyThreshold != nil {
		threshold = *s.FuzzyThreshold
	}
	updated, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:                    sp.ID,
		FuzzyThreshold:        threshold,
		MtOnUpload:            sp.MtOnUpload,
		TmOnUpload:            sp.TmOnUpload,
		QaProfileID:           sp.QaProfileID,
		SublanguagePenaltyPct: settings.ClampPctPtr(s.SublanguagePenaltyPct),
		NearExactPenaltyPct:   settings.ClampPctPtr(s.NearExactPenaltyPct),
		TmLookupOrder:         s.TMLookupOrder,
	})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("update scoring: %w", err)
	}
	return updated, nil
}

// createOwnedSettingsRow creates the settings row a new profile owns. Only the
// TM-relevant columns are meaningful on it; the rest stay at their defaults.
func createOwnedSettingsRow(ctx context.Context, q db.Querier, tenantID int64, s Scoring) (db.SettingsProfile, error) {
	if err := validateScoring(s); err != nil {
		return db.SettingsProfile{}, err
	}
	threshold := defaultFuzzyThreshold
	if s.FuzzyThreshold != nil {
		threshold = *s.FuzzyThreshold
	}
	sp, err := q.CreateSettingsProfile(ctx, db.CreateSettingsProfileParams{
		TenantID: tenantID, FuzzyThreshold: threshold,
	})
	if err != nil {
		return db.SettingsProfile{}, fmt.Errorf("create settings profile: %w", err)
	}
	if s.SublanguagePenaltyPct != nil || s.NearExactPenaltyPct != nil || s.TMLookupOrder != nil {
		return applyScoring(ctx, q, sp, Scoring{
			SublanguagePenaltyPct: s.SublanguagePenaltyPct,
			NearExactPenaltyPct:   s.NearExactPenaltyPct,
			TMLookupOrder:         s.TMLookupOrder,
		})
	}
	return sp, nil
}

// Create makes a new profile with optional scoring and TM rows. Must run inside
// the caller's transaction. A duplicate name surfaces as a unique violation for
// the handler to map to 409.
func Create(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64, name, description string, s Scoring, tms []settings.TMAssignmentInput) (db.TmProfile, error) {
	name, err := validateName(name)
	if err != nil {
		return db.TmProfile{}, err
	}
	sp, err := createOwnedSettingsRow(ctx, q, tenantID, s)
	if err != nil {
		return db.TmProfile{}, err
	}
	tp, err := q.CreateTMProfile(ctx, db.CreateTMProfileParams{
		TenantID: tenantID, SettingsProfileID: sp.ID,
		Name: name, Description: strings.TrimSpace(description), CreatedBy: createdBy,
	})
	if err != nil {
		return db.TmProfile{}, err
	}
	if tms != nil {
		if _, err := settings.ApplyTMAssignments(ctx, q, tenantID, sp.ID, tms); err != nil {
			return db.TmProfile{}, err
		}
	}
	return tp, nil
}

// CreateFromSettingsProfile snapshots the TM scope of a project's or template's
// settings row — the profile it follows, or its own rows when unlinked — into a
// new profile: TM rows plus the match scoring. Must run inside the
// caller's transaction; the caller links the source afterwards if asked to.
func CreateFromSettingsProfile(ctx context.Context, q db.Querier, tenantID int64, createdBy *int64, name, description string, srcSettingsProfileID int64) (db.TmProfile, error) {
	scope, err := q.GetTMScopeSettingsProfile(ctx, srcSettingsProfileID)
	if err != nil {
		return db.TmProfile{}, fmt.Errorf("get TM scope profile: %w", err)
	}
	tp, err := Create(ctx, q, tenantID, createdBy, name, description, Scoring{
		FuzzyThreshold:        &scope.FuzzyThreshold,
		SublanguagePenaltyPct: &scope.SublanguagePenaltyPct,
		NearExactPenaltyPct:   &scope.NearExactPenaltyPct,
		TMLookupOrder:         &scope.TmLookupOrder,
	}, nil)
	if err != nil {
		return db.TmProfile{}, err
	}
	if err := settings.CopyTMAssignments(ctx, q, scope.ID, tp.SettingsProfileID); err != nil {
		return db.TmProfile{}, err
	}
	return tp, nil
}

// UpdateInput edits a profile. Nil fields keep their stored values; a nil TMs
// leaves the TM rows alone, a non-nil one replaces them.
type UpdateInput struct {
	Name        *string
	Description *string
	Scoring     Scoring
	TMs         []settings.TMAssignmentInput
}

// Update applies in to an existing profile of the tenant. Must run inside the
// caller's transaction. A duplicate name surfaces as a unique violation.
func Update(ctx context.Context, q db.Querier, tenantID int64, existing db.TmProfile, in UpdateInput) error {
	name := existing.Name
	if in.Name != nil {
		var err error
		if name, err = validateName(*in.Name); err != nil {
			return err
		}
	}
	description := existing.Description
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	if err := validateScoring(in.Scoring); err != nil {
		return err
	}
	if _, err := q.UpdateTMProfile(ctx, db.UpdateTMProfileParams{
		ID: existing.ID, TenantID: tenantID, Name: name, Description: description,
	}); err != nil {
		return err
	}
	sp, err := q.GetSettingsProfile(ctx, existing.SettingsProfileID)
	if err != nil {
		return fmt.Errorf("get TM profile settings: %w", err)
	}
	if _, err := applyScoring(ctx, q, sp, in.Scoring); err != nil {
		return err
	}
	if in.TMs != nil {
		if _, err := settings.ApplyTMAssignments(ctx, q, tenantID, existing.SettingsProfileID, in.TMs); err != nil {
			return err
		}
	}
	return nil
}

// InUseError refuses a delete while projects or templates follow the profile;
// they would silently lose their TMs otherwise.
type InUseError struct{ Projects, Templates int32 }

func (e InUseError) Error() string {
	return fmt.Sprintf("TM profile is used by %d project(s) and %d template(s); detach them first", e.Projects, e.Templates)
}

// Delete soft-deletes a profile of the tenant. InUseError while anything
// follows it; pgx.ErrNoRows when it is not in the tenant.
func Delete(ctx context.Context, q db.Querier, tenantID, id int64) error {
	if _, err := q.GetTMProfile(ctx, db.GetTMProfileParams{ID: id, TenantID: tenantID}); err != nil {
		return err
	}
	usage, err := q.CountTMProfileUsage(ctx, db.CountTMProfileUsageParams{TmProfileID: &id, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("count TM profile usage: %w", err)
	}
	if usage.ProjectCount > 0 || usage.TemplateCount > 0 {
		return InUseError{Projects: usage.ProjectCount, Templates: usage.TemplateCount}
	}
	if err := q.SoftDeleteTMProfile(ctx, db.SoftDeleteTMProfileParams{ID: id, TenantID: tenantID}); err != nil {
		return fmt.Errorf("soft-delete TM profile: %w", err)
	}
	return nil
}
