package mcp

import (
	"context"
	"fmt"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/settings"
	"openloc/internal/sliceutil"
	tmpl "openloc/internal/template"
)

// Templates. A project copies its template's configuration when it is
// created, so an edit to a template never changes an existing
// project; previews count the projects made from it for context.

func configTemplateSummary(t db.Template) TemplateSummary {
	targets := sliceutil.NonNil(t.TargetLanguages)
	return TemplateSummary{
		TemplateID: t.ID, Name: t.Name, ProjectName: ptr.Deref(t.ProjectName),
		SourceLanguage: ptr.Deref(t.SourceLanguage), TargetLanguages: targets,
		CustomerID: ptr.Deref(t.CustomerID), Domain: ptr.Deref(t.Domain),
	}
}

func (b *directBackend) ListTemplates(ctx context.Context, in ListTemplatesIn) (TemplateList, error) {
	tenantID, _ := scope(ctx)
	limit := clampLimit(in.Limit)
	params := db.ListTemplatesFilteredParams{TenantID: tenantID, Cursor: in.Cursor, Lim: int32(limit + 1)}
	if q := strings.TrimSpace(in.Query); q != "" {
		params.Q = &q
	}
	rows, err := b.q.ListTemplatesFiltered(ctx, params)
	if err != nil {
		return TemplateList{}, fmt.Errorf("list templates: %w", err)
	}
	out := TemplateList{Templates: make([]TemplateSummary, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		out.HasMore = true
		out.NextCursor = rows[len(rows)-1].ID
	}
	for _, t := range rows {
		out.Templates = append(out.Templates, configTemplateSummary(t))
	}
	return out, nil
}

func (b *directBackend) GetTemplate(ctx context.Context, in GetTemplateIn) (TemplateDetail, error) {
	tenantID, _ := scope(ctx)
	t, err := b.q.GetTemplate(ctx, db.GetTemplateParams{ID: in.TemplateID, TenantID: tenantID})
	if err != nil {
		return TemplateDetail{}, configErr(err)
	}
	profile, err := b.q.GetSettingsProfile(ctx, t.SettingsProfileID)
	if err != nil {
		return TemplateDetail{}, fmt.Errorf("get settings profile: %w", err)
	}
	return b.templateDetail(ctx, tenantID, t, profile)
}

// templateDetail shows the effective settings: a template that follows a TM
// profile reports that profile's scoring, as GET /api/templates/{id} does.
func (b *directBackend) templateDetail(ctx context.Context, tenantID int64, t db.Template, own db.SettingsProfile) (TemplateDetail, error) {
	tmScope, err := settings.ResolveTMScope(ctx, b.q, tenantID, own)
	if err != nil {
		return TemplateDetail{}, fmt.Errorf("resolve TM settings: %w", err)
	}
	p := tmScope.Effective(own)
	steps, err := b.q.ListSettingsWorkflowSteps(ctx, own.ID)
	if err != nil {
		return TemplateDetail{}, fmt.Errorf("list template workflow steps: %w", err)
	}
	created, err := tmpl.ProjectsFrom(ctx, b.q, tenantID, t.ID)
	if err != nil {
		return TemplateDetail{}, err
	}
	out := TemplateDetail{
		TemplateSummary: configTemplateSummary(t),
		FuzzyThreshold:  p.FuzzyThreshold, MtOnUpload: p.MtOnUpload, TmOnUpload: p.TmOnUpload,
		AutoComplete: p.AutoComplete, InlineHTML: p.InlineHtml, SublanguagePenaltyPct: p.SublanguagePenaltyPct,
		NearExactPenaltyPct: p.NearExactPenaltyPct, QAProfileID: ptr.Deref(p.QaProfileID),
		MTRouterID: ptr.Deref(p.MtRouterID), MTRouterName: ptr.Deref(settings.MTRouterName(ctx, b.q, tenantID, p.MtRouterID)),
		WorkflowID: ptr.Deref(p.WorkflowID), WorkflowName: ptr.Deref(settings.WorkflowName(ctx, b.q, tenantID, p.WorkflowID)),
		WorkflowSteps: make([]string, 0, len(steps)), ProjectsCreated: created,
	}
	if tmScope.Linked != nil {
		out.TMProfileID, out.TMProfileName = tmScope.Linked.ID, tmScope.Linked.Name
	}
	for _, s := range steps {
		out.WorkflowSteps = append(out.WorkflowSteps, s.Name)
	}
	return out, nil
}

func (b *directBackend) CreateTemplate(ctx context.Context, in CreateTemplateIn) (TemplateDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("creating a template"); err != nil {
		return TemplateDetail{}, err
	}
	t, profile, err := tmpl.Create(ctx, b.q, b.pool, tenantID, tmpl.CreateInput{
		Name: in.Name, MtOnUpload: in.MtOnUpload, TmOnUpload: in.TmOnUpload, FuzzyThreshold: in.FuzzyThreshold,
		ProjectName: ptr.NilIfZero(in.ProjectName), SourceLanguage: ptr.NilIfZero(in.SourceLanguage),
		TargetLanguages: in.TargetLanguages, CustomerID: ptr.NilIfZero(in.CustomerID), Domain: ptr.NilIfZero(in.Domain),
		Setup: settings.Setup{
			AutoComplete: in.AutoComplete, InlineHTML: in.InlineHTML, WorkflowID: ptr.NilIfZero(in.WorkflowID),
			MTRouterID: ptr.NilIfZero(in.MTRouterID), QaProfileID: ptr.NilIfZero(in.QAProfileID),
			TMProfileID: ptr.NilIfZero(in.TMProfileID),
		},
	})
	if err != nil {
		return TemplateDetail{}, configErr(err)
	}
	return b.templateDetail(ctx, tenantID, t, profile)
}

func (b *directBackend) DuplicateTemplate(ctx context.Context, in DuplicateTemplateIn) (TemplateDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("duplicating a template"); err != nil {
		return TemplateDetail{}, err
	}
	t, profile, err := tmpl.Duplicate(ctx, b.q, b.pool, tenantID, in.TemplateID)
	if err != nil {
		return TemplateDetail{}, configErr(err)
	}
	return b.templateDetail(ctx, tenantID, t, profile)
}

// configNullableID maps an optional tool id onto the service's tri-state: omitted
// keeps, 0 clears, a positive id sets.
func configNullableID(id *int64) tmpl.NullableID {
	if id == nil {
		return tmpl.NullableID{}
	}
	return tmpl.NullableID{Set: true, ID: ptr.NilIfZero(*id)}
}

func (in UpdateTemplateIn) service() tmpl.UpdateInput {
	return tmpl.UpdateInput{
		Name: in.Name, MtOnUpload: in.MtOnUpload, TmOnUpload: in.TmOnUpload, AutoComplete: in.AutoComplete,
		InlineHTML: in.InlineHTML, FuzzyThreshold: in.FuzzyThreshold, SublanguagePenaltyPct: in.SublanguagePenaltyPct,
		NearExactPenaltyPct: in.NearExactPenaltyPct, QAProfile: configNullableID(in.QAProfileID),
		MTRouter: configNullableID(in.MTRouterID), LinkTMProfile: in.TMProfileID, Customer: configNullableID(in.CustomerID),
		ProjectName: in.ProjectName, Domain: in.Domain,
		SourceLanguage: in.SourceLanguage, TargetLanguages: in.TargetLanguages,
	}
}

// templateEffects says who a template change reaches: only projects created
// from it from now on.
func (b *directBackend) templateEffects(ctx context.Context, tenantID, id int64) ([]string, error) {
	n, err := tmpl.ProjectsFrom(ctx, b.q, tenantID, id)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return []string{fmt.Sprintf("%s were created from it; they keep their own settings.", configPlural(n, "project"))}, nil
}

func (b *directBackend) PreviewUpdateTemplate(ctx context.Context, in UpdateTemplateIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	if in.TMProfileID != nil && *in.TMProfileID <= 0 {
		return Preview{}, invalidf("tm_profile_id must be a TM profile id; leaving a TM profile is done in the template settings")
	}
	plan, err := tmpl.PlanUpdate(ctx, b.q, tenantID, in.TemplateID, in.service())
	if err != nil {
		return Preview{}, configErr(err)
	}
	effects, err := b.templateEffects(ctx, tenantID, plan.Template.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Update the template %q. New projects made from it start with the change.", plan.Template.Name),
		Effects: effects,
	}, nil
}

func (b *directBackend) UpdateTemplate(ctx context.Context, in UpdateTemplateIn) (TemplateDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("updating a template"); err != nil {
		return TemplateDetail{}, err
	}
	if in.TMProfileID != nil && *in.TMProfileID <= 0 {
		return TemplateDetail{}, invalidf("tm_profile_id must be a TM profile id; leaving a TM profile is done in the template settings")
	}
	t, profile, err := tmpl.Update(ctx, b.q, b.pool, tenantID, in.TemplateID, in.service())
	if err != nil {
		return TemplateDetail{}, configErr(err)
	}
	return b.templateDetail(ctx, tenantID, t, profile)
}
