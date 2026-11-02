package mcp

import (
	"context"
	"fmt"

	"openloc/internal/segmentation"
	"openloc/internal/segmentationapi"
)

// Segmentation profiles: the same service the REST routes run. A profile is a
// snapshot source, not a reference: an upload copies its project's profile's
// rules, so a change reaches only the uploads after it.

const segmentationEffect = "Files already uploaded keep their segments; uploads from now on use the change."

func (b *directBackend) segmentation() *segmentationapi.Service {
	return segmentationapi.NewService(b.q, b.pool)
}

func segmentationSummary(p segmentationapi.Profile) SegmentationProfileSummary {
	return SegmentationProfileSummary{
		ProfileID: p.ID, Name: p.Name, SourceLanguages: p.SourceLanguages, Builtin: p.Builtin, BuiltinKey: p.BuiltinKey,
		Suggested: p.Suggested, Mode: string(p.Definition.Mode), Rules: len(p.Definition.Rules),
		Abbreviations: len(p.Definition.Abbreviations) + len(p.Definition.NumberAbbreviations),
	}
}

func segmentationDetail(p segmentationapi.Profile) SegmentationProfileDetail {
	return SegmentationProfileDetail{
		SegmentationProfileSummary: segmentationSummary(p), Description: p.Description,
		BasedOnProfileID: p.BasedOn, Definition: p.Definition,
	}
}

func (b *directBackend) ListSegmentationProfiles(ctx context.Context, in ListSegmentationProfilesIn) (SegmentationProfileList, error) {
	tenantID, _ := scope(ctx)
	profiles, err := b.segmentation().List(ctx, tenantID, in.SourceLanguage)
	if err != nil {
		return SegmentationProfileList{}, configErr(err)
	}
	out := SegmentationProfileList{Profiles: make([]SegmentationProfileSummary, 0, len(profiles))}
	for _, p := range profiles {
		out.Profiles = append(out.Profiles, segmentationSummary(p))
	}
	return out, nil
}

func (b *directBackend) GetSegmentationProfile(ctx context.Context, in GetSegmentationProfileIn) (SegmentationProfileDetail, error) {
	tenantID, _ := scope(ctx)
	p, err := b.segmentation().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return SegmentationProfileDetail{}, configErr(err)
	}
	return segmentationDetail(p), nil
}

func (b *directBackend) PreviewSegmentation(ctx context.Context, in PreviewSegmentationIn) (SegmentationPreview, error) {
	tenantID, _ := scope(ctx)
	p, err := b.segmentation().Preview(ctx, tenantID, segmentationapi.PreviewInput{RuleSet: in.Definition.ruleSet(), ID: in.ProfileID, Text: in.Text})
	if err != nil {
		return SegmentationPreview{}, configErr(err)
	}
	out := SegmentationPreview{Segments: p.Segments, Decisions: []SegmentationPreviewDecision{}}
	for _, piece := range p.Pieces {
		if d := piece.Decision; d != nil {
			out.Decisions = append(out.Decisions, SegmentationPreviewDecision{
				After: piece.Text, Break: d.Break, Source: string(d.Source), Rule: d.Rule, Word: d.Word,
			})
		}
	}
	return out, nil
}

func (b *directBackend) ExportSegmentationSRX(ctx context.Context, in GetSegmentationProfileIn) (SegmentationSRX, error) {
	tenantID, _ := scope(ctx)
	name, data, err := b.segmentation().ExportSRX(ctx, tenantID, in.ProfileID)
	if err != nil {
		return SegmentationSRX{}, configErr(err)
	}
	return SegmentationSRX{FileName: name, SRX: string(data)}, nil
}

func (b *directBackend) ReadSegmentationSRX(_ context.Context, in ReadSegmentationSRXIn) (segmentation.SRXImport, error) {
	imp, err := segmentationapi.ParseSRX([]byte(in.SRX))
	if err != nil {
		return segmentation.SRXImport{}, configErr(err)
	}
	return imp, nil
}

func (b *directBackend) CreateSegmentationProfile(ctx context.Context, in CreateSegmentationProfileIn) (SegmentationProfileDetail, error) {
	tenantID, userID := scope(ctx)
	if err := b.needPool("creating a segmentation profile"); err != nil {
		return SegmentationProfileDetail{}, err
	}
	p, err := b.segmentation().Create(ctx, tenantID, userID, segmentationapi.Input{
		Name: in.Name, SourceLanguages: in.SourceLanguages, Definition: in.Definition.ruleSet(), From: in.FromProfileID,
	})
	if err != nil {
		return SegmentationProfileDetail{}, configErr(err)
	}
	return segmentationDetail(p), nil
}

// segmentationUpdate is the full replacement an update sends: the stored
// profile with the fields the call gives.
func (b *directBackend) segmentationUpdate(ctx context.Context, in UpdateSegmentationProfileIn) (segmentationapi.Profile, segmentationapi.Input, error) {
	tenantID, _ := scope(ctx)
	cur, err := b.segmentation().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return segmentationapi.Profile{}, segmentationapi.Input{}, configErr(err)
	}
	if cur.Builtin {
		return segmentationapi.Profile{}, segmentationapi.Input{}, invalidf("built-in segmentation profiles are read-only; copy it with %s and from_profile_id", toolCreateSegmentationProfile)
	}
	up := segmentationapi.Input{Name: cur.Name, SourceLanguages: cur.SourceLanguages, Definition: &cur.Definition, UpdatedAt: &cur.UpdatedAt}
	if in.Name != nil {
		up.Name = *in.Name
	}
	if in.SourceLanguages != nil {
		up.SourceLanguages = *in.SourceLanguages
	}
	if in.Definition != nil {
		up.Definition = in.Definition.ruleSet()
	}
	if err := segmentationapi.ValidateInput(up); err != nil {
		return segmentationapi.Profile{}, segmentationapi.Input{}, configErr(err)
	}
	return cur, up, nil
}

// segmentationUses says how many projects and templates use a profile.
func (b *directBackend) segmentationUses(ctx context.Context, id int64) (string, error) {
	tenantID, _ := scope(ctx)
	projects, templates, err := b.segmentation().Uses(ctx, tenantID, id)
	if err != nil {
		return "", configErr(err)
	}
	return fmt.Sprintf("%s and %s use it.", segmentationapi.Plural(projects, "project"), segmentationapi.Plural(templates, "template")), nil
}

func (b *directBackend) PreviewUpdateSegmentationProfile(ctx context.Context, in UpdateSegmentationProfileIn) (Preview, error) {
	cur, _, err := b.segmentationUpdate(ctx, in)
	if err != nil {
		return Preview{}, err
	}
	uses, err := b.segmentationUses(ctx, cur.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Summary: fmt.Sprintf("Change the segmentation profile %q.", cur.Name), Effects: []string{uses, segmentationEffect}}, nil
}

func (b *directBackend) UpdateSegmentationProfile(ctx context.Context, in UpdateSegmentationProfileIn) (SegmentationProfileDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating a segmentation profile"); err != nil {
		return SegmentationProfileDetail{}, err
	}
	_, up, err := b.segmentationUpdate(ctx, in)
	if err != nil {
		return SegmentationProfileDetail{}, err
	}
	p, err := b.segmentation().Update(ctx, tenantID, in.ProfileID, up)
	if err != nil {
		return SegmentationProfileDetail{}, configErr(err)
	}
	return segmentationDetail(p), nil
}

func (b *directBackend) PreviewDeleteSegmentationProfile(ctx context.Context, in DeleteSegmentationProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	cur, err := b.segmentation().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	if cur.Builtin {
		return Preview{}, invalidf("built-in segmentation profiles cannot be deleted")
	}
	uses, err := b.segmentationUses(ctx, cur.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the segmentation profile %q.", cur.Name),
		Effects: []string{uses, "Projects that use it lose it: their next upload asks for another profile. Files already uploaded keep their segments. A profile a template uses cannot be deleted."},
	}, nil
}

func (b *directBackend) DeleteSegmentationProfile(ctx context.Context, in DeleteSegmentationProfileIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("deleting a segmentation profile"); err != nil {
		return DeletedConfig{}, err
	}
	cur, err := b.segmentation().Get(ctx, tenantID, in.ProfileID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := b.segmentation().Delete(ctx, tenantID, in.ProfileID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: cur.ID, Name: cur.Name}, nil
}
