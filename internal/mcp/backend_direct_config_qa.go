package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/qa"
)

// QA profiles: the checks a project's QA runs. A project and a template point
// at a profile rather than copying it, so an edit to its rules applies to the
// next QA run of every project that uses it.

func (b *directBackend) ListQAProfiles(ctx context.Context, _ ListQAProfilesIn) (QAProfileList, error) {
	tenantID, _ := scope(ctx)
	rows, err := b.q.ListQAProfiles(ctx, tenantID)
	if err != nil {
		return QAProfileList{}, fmt.Errorf("list QA profiles: %w", err)
	}
	out := QAProfileList{Profiles: make([]QAProfileSummary, 0, len(rows))}
	for _, p := range rows {
		out.Profiles = append(out.Profiles, QAProfileSummary{QAProfileID: p.ID, Name: p.Name, Description: p.Description})
	}
	return out, nil
}

func (b *directBackend) GetQAProfile(ctx context.Context, in GetQAProfileIn) (QAProfileDetail, error) {
	tenantID, _ := scope(ctx)
	p, err := b.q.GetQAProfile(ctx, db.GetQAProfileParams{ID: in.QAProfileID, TenantID: tenantID})
	if err != nil {
		return QAProfileDetail{}, configErr(err)
	}
	return b.qaProfileDetail(ctx, p)
}

// qaProfileDetail reads the rules of a profile the caller already resolved in
// its tenant.
func (b *directBackend) qaProfileDetail(ctx context.Context, p db.QaProfile) (QAProfileDetail, error) {
	rules, err := b.q.ListQARules(ctx, p.ID)
	if err != nil {
		return QAProfileDetail{}, fmt.Errorf("list QA rules: %w", err)
	}
	used, err := qa.ProfileUsage(ctx, b.q, p.ID)
	if err != nil {
		return QAProfileDetail{}, err
	}
	out := QAProfileDetail{
		QAProfileSummary: QAProfileSummary{QAProfileID: p.ID, Name: p.Name, Description: p.Description},
		Rules:            make([]QARule, 0, len(rules)), UsedBy: used,
	}
	for _, r := range rules {
		cfg := map[string]any{}
		if len(r.Config) > 0 {
			if err := json.Unmarshal(r.Config, &cfg); err != nil {
				return QAProfileDetail{}, fmt.Errorf("decode QA rule config: %w", err)
			}
		}
		out.Rules = append(out.Rules, QARule{CheckType: r.CheckType, Enabled: r.Enabled, Severity: r.Severity, Config: cfg})
	}
	return out, nil
}

func configQARules(in []QARuleIn) ([]qa.RuleInput, error) {
	out := make([]qa.RuleInput, 0, len(in))
	for _, r := range in {
		rule := qa.RuleInput{CheckType: r.CheckType, Enabled: r.Enabled, Severity: r.Severity}
		if r.Config != nil {
			raw, err := json.Marshal(r.Config)
			if err != nil {
				return nil, invalidf("config of %s is not a JSON object", r.CheckType)
			}
			rule.Config = raw
		}
		out = append(out, rule)
	}
	return out, nil
}

func (b *directBackend) CreateQAProfile(ctx context.Context, in CreateQAProfileIn) (QAProfileDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("creating a QA profile"); err != nil {
		return QAProfileDetail{}, err
	}
	rules, err := configQARules(in.Rules)
	if err != nil {
		return QAProfileDetail{}, err
	}
	p, _, err := qa.CreateProfileWithRules(ctx, b.pool, tenantID, qa.ProfileInput{Name: in.Name, Description: in.Description}, rules)
	if err != nil {
		return QAProfileDetail{}, configErr(err)
	}
	return b.qaProfileDetail(ctx, p)
}

// configQAEffects says who a profile change reaches.
func configQAEffects(used int64) []string {
	if used == 0 {
		return nil
	}
	return []string{fmt.Sprintf("Used by %s (projects and templates); their next QA run uses the change.", configPlural(used, "settings profile"))}
}

func (b *directBackend) PreviewUpdateQAProfile(ctx context.Context, in UpdateQAProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	p, err := qa.ValidateProfileUpdate(ctx, b.q, tenantID, in.QAProfileID, qa.ProfileInput{Name: in.Name, Description: in.Description})
	if err != nil {
		return Preview{}, configErr(err)
	}
	used, err := qa.ProfileUsage(ctx, b.q, p.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Summary: fmt.Sprintf("Rename or re-describe the QA profile %q.", p.Name), Effects: configQAEffects(used)}, nil
}

func (b *directBackend) UpdateQAProfile(ctx context.Context, in UpdateQAProfileIn) (QAProfileDetail, error) {
	tenantID, _ := scope(ctx)
	p, err := qa.UpdateProfile(ctx, b.q, tenantID, in.QAProfileID, qa.ProfileInput{Name: in.Name, Description: in.Description})
	if err != nil {
		return QAProfileDetail{}, configErr(err)
	}
	return b.qaProfileDetail(ctx, p)
}

func (b *directBackend) PreviewUpsertQARules(ctx context.Context, in UpsertQARulesIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	rules, err := configQARules(in.Rules)
	if err != nil {
		return Preview{}, err
	}
	p, params, err := qa.ValidateRules(ctx, b.q, tenantID, in.QAProfileID, rules)
	if err != nil {
		return Preview{}, configErr(err)
	}
	used, err := qa.ProfileUsage(ctx, b.q, p.ID)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Summary: fmt.Sprintf("Set %s on the QA profile %q.", configPlural(int64(len(params)), "rule"), p.Name),
		Effects: configQAEffects(used),
	}, nil
}

func (b *directBackend) UpsertQARules(ctx context.Context, in UpsertQARulesIn) (QAProfileDetail, error) {
	tenantID, _ := scope(ctx)
	if err := b.needPool("changing QA rules"); err != nil {
		return QAProfileDetail{}, err
	}
	rules, err := configQARules(in.Rules)
	if err != nil {
		return QAProfileDetail{}, err
	}
	if _, err := qa.UpsertRules(ctx, b.q, b.pool, tenantID, in.QAProfileID, rules); err != nil {
		return QAProfileDetail{}, configErr(err)
	}
	return b.GetQAProfile(ctx, GetQAProfileIn{QAProfileID: in.QAProfileID})
}

func (b *directBackend) PreviewDeleteQAProfile(ctx context.Context, in DeleteQAProfileIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	p, err := qa.CheckDeleteProfile(ctx, b.q, tenantID, in.QAProfileID)
	if err != nil {
		return Preview{}, configErr(err)
	}
	return Preview{Summary: fmt.Sprintf("Delete the QA profile %q and its rules. No project or template uses it.", p.Name)}, nil
}

func (b *directBackend) DeleteQAProfile(ctx context.Context, in DeleteQAProfileIn) (DeletedConfig, error) {
	tenantID, _ := scope(ctx)
	p, err := qa.CheckDeleteProfile(ctx, b.q, tenantID, in.QAProfileID)
	if err != nil {
		return DeletedConfig{}, configErr(err)
	}
	if err := qa.DeleteProfile(ctx, b.q, tenantID, p.ID); err != nil {
		return DeletedConfig{}, configErr(err)
	}
	return DeletedConfig{ID: p.ID, Name: p.Name}, nil
}
