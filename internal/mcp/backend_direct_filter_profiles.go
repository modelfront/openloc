package mcp

import (
	"context"
	"fmt"

	"openloc/internal/filterprofile"
)

// Filter profiles: the same service the REST routes run. An upload copies
// its profile's options onto the file, so a change reaches only the uploads
// after it.
const filterProfileEffect = "Files already uploaded keep the options they were read with; uploads from now on use the change."

func (b *directBackend) filterProfiles() *filterprofile.Service {
	return filterprofile.NewService(b.q, b.pool)
}

func filterProfileDetail(p filterprofile.Profile) FilterProfileDetail {
	return FilterProfileDetail{
		ProfileID: p.ID, Name: p.Name, Format: p.Format, Builtin: p.Builtin, OrgDefault: p.OrgDefault, BuiltinKey: p.BuiltinKey,
		Description: p.Description, BasedOnProfileID: p.BasedOn, Options: p.Options,
	}
}

func (b *directBackend) ListFilterProfiles(ctx context.Context, in ListFilterProfilesIn) (FilterProfileList, error) {
	tenantID, _ := scope(ctx)
	profiles, err := b.filterProfiles().List(ctx, tenantID, in.Format)
	if err != nil {
		return FilterProfileList{}, configErr(err)
	}
	out := FilterProfileList{Profiles: make([]FilterProfileDetail, 0, len(profiles))}
	for _, p := range profiles {
		out.Profiles = append(out.Profiles, filterProfileDetail(p))
	}
	return out, nil
}

func (b *directBackend) GetFilterProfile(ctx context.Context, in GetFilterProfileIn) (FilterProfileDetail, error) {
	tenantID, _ := scope(ctx)
	p, err := b.filterProfiles().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return FilterProfileDetail{}, configErr(err)
	}
	return filterProfileDetail(p), nil
}

func (b *directBackend) CreateFilterProfile(ctx context.Context, in CreateFilterProfileIn) (FilterProfileDetail, error) {
	tenantID, userID := scope(ctx)
	if err := b.needPool("creating a filter profile"); err != nil {
		return FilterProfileDetail{}, err
	}
	p, err := b.filterProfiles().Create(ctx, tenantID, userID, filterprofile.Input{
		Name: in.Name, Format: in.Format, Options: in.Options.options(), From: in.FromProfileID,
	})
	if err != nil {
		return FilterProfileDetail{}, configErr(err)
	}
	return filterProfileDetail(p), nil
}

// filterProfileUpdate is the full replacement an update sends: the stored
// profile with the fields the call gives.
func (b *directBackend) filterProfileUpdate(ctx context.Context, in UpdateFilterProfileIn) (filterprofile.Profile, filterprofile.Input, error) {
	tenantID, _ := scope(ctx)
	cur, err := b.filterProfiles().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return filterprofile.Profile{}, filterprofile.Input{}, configErr(err)
	}
	if cur.Builtin {
		return filterprofile.Profile{}, filterprofile.Input{}, invalidf("built-in filter profiles are read-only; copy it with %s and from_profile_id", toolCreateFilterProfile)
	}
	up := filterprofile.Input{Name: cur.Name, Options: &cur.Options, UpdatedAt: &cur.UpdatedAt}
	if in.Name != nil {
		up.Name = *in.Name
	}
	if in.Options != nil {
		up.Options = in.Options.options()
	}
	return cur, up, nil
}

// filterProfileUses says how many projects and templates use a profile.
func (b *directBackend) filterProfileUses(ctx context.Context, id int64) (string, error) {
	tenantID, _ := scope(ctx)
	projects, templates, err := b.filterProfiles().Uses(ctx, tenantID, id)
	if err != nil {
		return "", configErr(err)
	}
	return fmt.Sprintf("%s and %s use it.", configPlural(projects, "project"), configPlural(templates, "template")), nil
}

func (b *directBackend) PreviewUpdateFilterProfile(ctx context.Context, in UpdateFilterProfileIn) (Preview, error) {
	cur, _, err := b.filterProfileUpdate(ctx, in)
	if err != nil {
		return Preview{}, err
	}
	uses, err := b.filterProfileUses(ctx, cur.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Summary: fmt.Sprintf("Change the filter profile %q.", cur.Name), Effects: []string{uses, filterProfileEffect}}, nil
}

func (b *directBackend) UpdateFilterProfile(ctx context.Context, in UpdateFilterProfileIn) (FilterProfileDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating a filter profile"); err != nil {
		return FilterProfileDetail{}, err
	}
	_, up, err := b.filterProfileUpdate(ctx, in)
	if err != nil {
		return FilterProfileDetail{}, err
	}
	p, err := b.filterProfiles().Update(ctx, tenantID, in.ProfileID, up)
	if err != nil {
		return FilterProfileDetail{}, configErr(err)
	}
	return filterProfileDetail(p), nil
}

func (b *directBackend) PreviewDeleteFilterProfile(ctx context.Context, in DeleteFilterProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	cur, err := b.filterProfiles().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	if cur.Builtin {
		return Preview{}, invalidf("built-in filter profiles cannot be deleted")
	}
	if cur.OrgDefault {
		return Preview{}, invalidf("this filter profile is your organization's default for its format; set another default with %s first", toolSetFilterDefault)
	}
	uses, err := b.filterProfileUses(ctx, cur.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the filter profile %q.", cur.Name),
		Effects: []string{uses, "Projects that use it go back to the format's Default. Files already uploaded keep their options. A profile a template uses cannot be deleted."},
	}, nil
}

func (b *directBackend) DeleteFilterProfile(ctx context.Context, in DeleteFilterProfileIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("deleting a filter profile"); err != nil {
		return DeletedConfig{}, err
	}
	cur, err := b.filterProfiles().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := b.filterProfiles().Delete(ctx, tenantID, in.ProfileID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: cur.ID, Name: cur.Name}, nil
}

func (b *directBackend) SetFilterProfileDefault(ctx context.Context, in SetFilterProfileDefaultIn) (FilterProfileDefaults, error) {
	tenantID, userID := scope(ctx)
	defaults, err := b.filterProfiles().SetDefault(ctx, tenantID, userID, filterprofile.DefaultInput{Format: in.Format, FilterProfileID: in.ProfileID})
	if err != nil {
		return FilterProfileDefaults{}, configErr(err)
	}
	out := FilterProfileDefaults{Defaults: make([]FilterProfileDefault, 0, len(defaults))}
	for _, d := range defaults {
		out.Defaults = append(out.Defaults, FilterProfileDefault{Format: d.Format, ProfileID: d.ID, Name: d.Name})
	}
	return out, nil
}
