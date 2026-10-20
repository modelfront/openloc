package mcp

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/tmprofile"
)

// TM profile tools: the same tmprofile service as /api/tm-profiles. Every
// method resolves the profile with the tenant-scoped GetTMProfile first; the
// settings row it owns is only read through that row.

func (b *directBackend) tmProfile(ctx context.Context, id int64) (db.TmProfile, error) {
	tenantID, _ := scope(ctx)
	p, err := b.q.GetTMProfile(ctx, db.GetTMProfileParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.TmProfile{}, resourceErr(err, "get TM profile")
	}
	return p, nil
}

func (b *directBackend) profileTMs(ctx context.Context, settingsProfileID int64) ([]TMProfileTM, error) {
	rows, err := b.q.ListSettingsTMAssignments(ctx, settingsProfileID)
	if err != nil {
		return nil, fmt.Errorf("list TM profile TMs: %w", err)
	}
	out := make([]TMProfileTM, 0, len(rows))
	for _, r := range rows {
		out = append(out, TMProfileTM{TMID: r.TmID, Mode: r.Mode, Priority: r.Priority, PenaltyPct: r.PenaltyPct})
	}
	return out, nil
}

func (b *directBackend) profileSummary(ctx context.Context, id int64) (TMProfileSummary, error) {
	tenantID, _ := scope(ctx)
	row, err := b.q.GetTMProfileWithStats(ctx, db.GetTMProfileWithStatsParams{ID: id, TenantID: tenantID})
	if err != nil {
		return TMProfileSummary{}, resourceErr(err, "get TM profile")
	}
	tms, err := b.profileTMs(ctx, row.SettingsProfileID)
	if err != nil {
		return TMProfileSummary{}, err
	}
	return TMProfileSummary{
		TMProfileID: row.ID, Name: row.Name, Description: row.Description,
		FuzzyThreshold: row.FuzzyThreshold, SublanguagePenaltyPct: row.SublanguagePenaltyPct,
		NearExactPenaltyPct: row.NearExactPenaltyPct, TMs: tms, UsageCount: row.UsageCount,
	}, nil
}

func (b *directBackend) ListTMProfiles(ctx context.Context, _ ListTMProfilesIn) (TMProfileList, error) {
	tenantID, _ := scope(ctx)
	rows, err := b.q.ListTMProfiles(ctx, tenantID)
	if err != nil {
		return TMProfileList{}, fmt.Errorf("list TM profiles: %w", err)
	}
	out := make([]TMProfileSummary, 0, len(rows))
	for _, r := range rows {
		tms, err := b.profileTMs(ctx, r.SettingsProfileID)
		if err != nil {
			return TMProfileList{}, err
		}
		out = append(out, TMProfileSummary{
			TMProfileID: r.ID, Name: r.Name, Description: r.Description,
			FuzzyThreshold: r.FuzzyThreshold, SublanguagePenaltyPct: r.SublanguagePenaltyPct,
			NearExactPenaltyPct: r.NearExactPenaltyPct, TMs: tms, UsageCount: r.UsageCount,
		})
	}
	return TMProfileList{Profiles: out, Count: len(out)}, nil
}

func toAssignmentInputs(in []TMProfileTMIn) []settings.TMAssignmentInput {
	out := make([]settings.TMAssignmentInput, 0, len(in))
	for _, t := range in {
		out = append(out, settings.TMAssignmentInput{TmID: t.TMID, Mode: t.Mode, Priority: t.Priority, PenaltyPct: t.PenaltyPct})
	}
	return out
}

var validTMModes = map[string]bool{"": true, "read": true, "write": true, "read_write": true}

func validateProfileTMs(in []TMProfileTMIn) error {
	for _, t := range in {
		if !validTMModes[t.Mode] {
			return invalidf("mode %q is not valid for TM %d; use read, write or read_write", t.Mode, t.TMID)
		}
	}
	return nil
}

func (b *directBackend) CreateTMProfile(ctx context.Context, in CreateTMProfileIn) (TMProfileSummary, error) {
	tenantID, userID := scope(ctx)
	if err := validateProfileTMs(in.TMs); err != nil {
		return TMProfileSummary{}, err
	}
	var id int64
	err := b.inResourceTx(ctx, "creating a TM profile", true, func(q db.Querier) error {
		tp, err := tmprofile.Create(ctx, q, tenantID, &userID, in.Name, in.Description, tmprofile.Scoring{
			FuzzyThreshold: in.FuzzyThreshold, SublanguagePenaltyPct: in.SublanguagePenaltyPct,
			NearExactPenaltyPct: in.NearExactPenaltyPct,
		}, toAssignmentInputs(in.TMs))
		id = tp.ID
		return err
	})
	if err != nil {
		return TMProfileSummary{}, resourceErr(err, "create TM profile")
	}
	return b.profileSummary(ctx, id)
}

// applyProfileUpdate runs tmprofile.Update; commit=false validates and rolls back.
func (b *directBackend) applyProfileUpdate(ctx context.Context, existing db.TmProfile, in UpdateTMProfileIn, commit bool) error {
	tenantID, _ := scope(ctx)
	upd := tmprofile.UpdateInput{
		Name: in.Name, Description: in.Description,
		Scoring: tmprofile.Scoring{
			FuzzyThreshold: in.FuzzyThreshold, SublanguagePenaltyPct: in.SublanguagePenaltyPct,
			NearExactPenaltyPct: in.NearExactPenaltyPct,
		},
	}
	if in.TMs != nil {
		if err := validateProfileTMs(*in.TMs); err != nil {
			return err
		}
		upd.TMs = toAssignmentInputs(*in.TMs)
	}
	err := b.inResourceTx(ctx, "updating a TM profile", commit, func(q db.Querier) error {
		return tmprofile.Update(ctx, q, tenantID, existing, upd)
	})
	return resourceErr(err, "update TM profile")
}

func (b *directBackend) PreviewUpdateTMProfile(ctx context.Context, in UpdateTMProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	existing, err := b.tmProfile(ctx, in.TMProfileID)
	if err != nil {
		return Preview{}, err
	}
	var changes []string
	if in.Name != nil {
		changes = append(changes, fmt.Sprintf("name → %q", strings.TrimSpace(*in.Name)))
	}
	if in.Description != nil {
		changes = append(changes, "description")
	}
	if in.FuzzyThreshold != nil {
		changes = append(changes, fmt.Sprintf("fuzzy threshold → %.2f", *in.FuzzyThreshold))
	}
	if in.SublanguagePenaltyPct != nil {
		changes = append(changes, fmt.Sprintf("sublanguage penalty → %d%%", *in.SublanguagePenaltyPct))
	}
	if in.NearExactPenaltyPct != nil {
		changes = append(changes, fmt.Sprintf("near-exact penalty → %d%%", *in.NearExactPenaltyPct))
	}
	if in.TMs != nil {
		changes = append(changes, fmt.Sprintf("TM list → %d TM(s)", len(*in.TMs)))
	}
	if len(changes) == 0 {
		return Preview{}, invalidf("nothing to update: set at least one field")
	}
	if err := b.applyProfileUpdate(ctx, existing, in, false); err != nil {
		return Preview{}, err
	}
	usage, err := b.q.CountTMProfileUsage(ctx, db.CountTMProfileUsageParams{TmProfileID: &existing.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count TM profile usage: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Change the TM profile %q: %s.", existing.Name, strings.Join(changes, ", "))}
	if usage.ProjectCount+usage.TemplateCount > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf(
			"Used by %d project(s) and %d template(s); their TM matches use the new settings from the next lookup.",
			usage.ProjectCount, usage.TemplateCount))
	} else {
		p.Effects = append(p.Effects, "No project or template follows this profile yet.")
	}
	return p, nil
}

func (b *directBackend) UpdateTMProfile(ctx context.Context, in UpdateTMProfileIn) (TMProfileSummary, error) {
	existing, err := b.tmProfile(ctx, in.TMProfileID)
	if err != nil {
		return TMProfileSummary{}, err
	}
	if err := b.applyProfileUpdate(ctx, existing, in, true); err != nil {
		return TMProfileSummary{}, err
	}
	return b.profileSummary(ctx, existing.ID)
}

func (b *directBackend) PreviewDeleteTMProfile(ctx context.Context, in DeleteTMProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	p, err := b.tmProfile(ctx, in.TMProfileID)
	if err != nil {
		return Preview{}, err
	}
	usage, err := b.q.CountTMProfileUsage(ctx, db.CountTMProfileUsageParams{TmProfileID: &p.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count TM profile usage: %w", err)
	}
	if usage.ProjectCount > 0 || usage.TemplateCount > 0 {
		return Preview{}, resourceErr(tmprofile.InUseError{Projects: usage.ProjectCount, Templates: usage.TemplateCount}, "delete TM profile")
	}
	tms, err := b.profileTMs(ctx, p.SettingsProfileID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the TM profile %q.", p.Name),
		Effects: []string{fmt.Sprintf("Its %d TM(s) are not deleted; only the bundle and its match settings go.", len(tms))},
	}, nil
}

func (b *directBackend) DeleteTMProfile(ctx context.Context, in DeleteTMProfileIn) (ResourceDeleted, error) {
	tenantID, _ := scope(ctx)
	p, err := b.tmProfile(ctx, in.TMProfileID)
	if err != nil {
		return ResourceDeleted{}, err
	}
	if err := tmprofile.Delete(ctx, b.q, tenantID, p.ID); err != nil {
		return ResourceDeleted{}, resourceErr(err, "delete TM profile")
	}
	return ResourceDeleted{Kind: "tm_profile", ID: p.ID, Name: p.Name}, nil
}
