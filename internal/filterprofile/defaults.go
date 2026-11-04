package filterprofile

import (
	"context"
	"fmt"
	"maps"
	"slices"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
)

// The org default per format is the pick a new project or template starts
// with when no template gives one. It is copied at creation, never read
// later: changing it leaves existing projects and templates as they are.

// OrgDefaults returns the tenant's default per format; a format left out
// uses its built-in Default.
func OrgDefaults(ctx context.Context, q db.Querier, tenantID int64) ([]Choice, error) {
	rows, err := q.ListFilterProfileDefaults(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list filter profile defaults: %w", err)
	}
	out := make([]Choice, 0, len(rows))
	for _, r := range rows {
		out = append(out, Choice{Format: r.Format, ID: r.FilterProfileID, Name: r.Name})
	}
	return out, nil
}

// SetOrgDefault makes id the tenant's default for format: a live profile of
// the tenant, or a built-in, of that format. A nil id goes back to the
// built-in Default.
func SetOrgDefault(ctx context.Context, q db.Querier, tenantID, userID int64, format string, id *int64) error {
	if !slices.Contains(Formats, format) {
		return httputil.NewClientError(fmt.Sprintf("%q has no filter profiles.", format))
	}
	if id == nil {
		if err := q.ClearFilterProfileDefault(ctx, db.ClearFilterProfileDefaultParams{TenantID: tenantID, Format: format}); err != nil {
			return fmt.Errorf("clear filter profile default: %w", err)
		}
		return nil
	}
	if err := Check(ctx, q, tenantID, *id, format); err != nil {
		return err
	}
	var updatedBy *int64
	if userID != 0 {
		updatedBy = &userID
	}
	n, err := q.SetFilterProfileDefault(ctx, db.SetFilterProfileDefaultParams{TenantID: tenantID, Format: format, FilterProfileID: *id, UpdatedBy: updatedBy})
	if err != nil {
		return fmt.Errorf("set filter profile default: %w", err)
	}
	if n == 0 {
		return ErrProfileNotFound // deleted since Check
	}
	return nil
}

// WithOrgDefaults returns choices with the tenant's default filled in for
// every format choices leaves out. A format set to nil stays the built-in
// Default. For a project or template created without a template.
func WithOrgDefaults(ctx context.Context, q db.Querier, tenantID int64, choices map[string]*int64) (map[string]*int64, error) {
	defaults, err := OrgDefaults(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	if len(defaults) == 0 {
		return choices, nil
	}
	out := maps.Clone(choices)
	if out == nil {
		out = make(map[string]*int64, len(defaults))
	}
	for _, d := range defaults {
		if _, set := out[d.Format]; !set {
			out[d.Format] = &d.ID
		}
	}
	return out, nil
}

// markOrgDefaults sets OrgDefault on the profiles that are the tenant's
// default for their format.
func markOrgDefaults(ctx context.Context, q db.Querier, tenantID int64, profiles []Profile) error {
	defaults, err := OrgDefaults(ctx, q, tenantID)
	if err != nil {
		return err
	}
	for _, d := range defaults {
		for i := range profiles {
			if profiles[i].ID == d.ID {
				profiles[i].OrgDefault = true
			}
		}
	}
	return nil
}

// SetDefault sets the tenant's default for one format and returns every
// format's.
func (s *Service) SetDefault(ctx context.Context, tenantID, userID int64, in DefaultInput) ([]Choice, error) {
	if err := SetOrgDefault(ctx, s.q, tenantID, userID, in.Format, in.FilterProfileID); err != nil {
		return nil, err
	}
	return OrgDefaults(ctx, s.q, tenantID)
}
