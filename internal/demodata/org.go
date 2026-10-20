package demodata

import (
	"context"
	"encoding/json"
	"fmt"

	"openloc/internal/analysis"
	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	"openloc/internal/tmprofile"
)

// organization is the pass Options.Organization adds after the projects:
// everything a page needs that the sample workspace leaves empty.
func (b *builder) organization() error {
	bt := b.built
	for _, step := range []func() error{
		b.aiReview,
		func() error {
			return b.runQAAll(bt.emailJobs["de"], bt.emailJobs["fr"], bt.ifuJobs["de"], bt.faqJobs["de"])
		},
		b.orgComments,
		b.templates,
		b.tmProfile,
		b.dashboards,
		b.notifications,
		// History before the analyses: their analysis.created events are today's.
		b.projectHistory,
		b.analyses,
		func() error { return WriteJobTimings(b.ctx, b.q, b.tenantID, b.allJobs()) },
		b.aiUsage,
		// Last: after every step that backdates history.
		func() error { return AlignTranslationDates(b.ctx, b.tx, b.tenantID) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) runQAAll(jobIDs ...int64) error {
	for _, id := range jobIDs {
		if err := b.runQA(id); err != nil {
			return err
		}
	}
	return nil
}

// allJobs is every job id, project by project.
func (b *builder) allJobs() []int64 {
	var out []int64
	for _, p := range b.res.Projects {
		out = append(out, p.JobIDs...)
	}
	return out
}

func (b *builder) orgComments() error {
	bt := b.built
	for _, c := range []struct {
		job          int64
		source, lang string
		author       int64
		text         string
	}{
		{bt.ifuJobs["de"], "The self-test takes about 15 seconds.", "de", b.linguistID, "Helix writes \"Selbsttest\" as one word in every chapter — kept it that way."},
		{bt.emailJobs["fr"], "Book now", "fr", b.frLinguist(b.linguistID), "\"Réserver\" fits the button; \"Réservez maintenant\" is too long for the mobile layout."},
		{bt.emailJobs["de"], "Our most-loved beach hotels", "de", b.ownerID, "Can we say \"Lieblingshotels am Strand\"? It sounds warmer."},
	} {
		if err := b.comment(c.job, c.source, c.lang, c.author, c.text); err != nil {
			return err
		}
	}
	return nil
}

// aiReview writes the result of the IFU project's AI review step: one segment
// approved as it is, one post-edited and approved, one post-edited for a
// person to check and one rejected, with the AI's notes and comments.
func (b *builder) aiReview() error {
	if b.ai.helix == nil {
		return nil
	}
	jobID := b.built.ifuJobs["de"]
	for _, o := range ifuAIReview {
		var tid int64
		var text string
		if err := b.tx.QueryRow(b.ctx, `
			SELECT t.id, COALESCE(t.active_text, '') FROM translations t
			JOIN segments sg ON sg.id = t.segment_id
			JOIN jobs j ON j.file_id = sg.file_id AND j.target_language = t.target_language
			WHERE j.id = $1 AND sg.source_text = $2`, jobID, o.source).Scan(&tid, &text); err != nil {
			return fmt.Errorf("ai review target %q: %w", o.source, err)
		}
		status := "needs_review"
		if o.outcome == "approved" || o.outcome == "ape_approved" {
			status = "auto_approved"
		}
		origin := "mt"
		if o.text != "" {
			text, origin = o.text, "ape"
		}
		if err := b.exec(`UPDATE translations SET status = $2, predict_outcome = $3, qe_note = $4, active_text = $5, origin = $6 WHERE id = $1`,
			tid, status, o.outcome, o.note, text, origin); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]string{"predict_outcome": o.outcome, "source": "claude", "model": aiClaudeModel, "note": o.note})
		if err != nil {
			return fmt.Errorf("ai review details: %w", err)
		}
		ev := db.InsertTranslationHistoryParams{TranslationID: tid, AiEngineID: b.ai.helix, Details: details}
		switch {
		case o.text != "":
			ev.Kind, ev.Text = "ape", &o.text
		case o.outcome == "approved":
			ev.Kind = "auto_approved"
		}
		if ev.Kind != "" {
			row, err := b.q.InsertTranslationHistory(b.ctx, ev)
			if err != nil {
				return fmt.Errorf("ai review history: %w", err)
			}
			if err := b.exec(`UPDATE translation_history SET created_at = $2 WHERE id = $1`, row.ID, b.daysAgo(1)); err != nil {
				return err
			}
		}
		if o.comment != "" {
			if _, err := b.q.CreateAIComment(b.ctx, db.CreateAICommentParams{TranslationID: tid, Content: o.comment}); err != nil {
				return fmt.Errorf("ai comment: %w", err)
			}
		}
	}
	return nil
}

// templates adds one template per customer workflow and bases the booking
// project on the Nordwind one.
func (b *builder) templates() error {
	bt := b.built
	engines, err := b.mtEngines(nil)
	if err != nil {
		return err
	}
	for _, t := range []struct {
		name, project, customer, domain string
		targets                         []string
		tm                              int64
		termBase                        bool
		workflow                        func(profileID int64) error
		applyTo                         *db.Project
	}{
		{templateNordwind, "Nordwind — New campaign", customerNordwind, "Travel", []string{"de", "fr"}, bt.tmNordwind, true,
			func(id int64) error { return b.templateSteps(id, b.lib.Standard.ID, b.lib.Translation, b.lib.Review) }, &bt.booking},
		{templateHelix, "Helix — New IFU revision", customerHelix, "Medical devices", []string{"de"}, bt.tmHelix, false,
			func(id int64) error {
				if !b.lib.HasAI {
					return b.templateSteps(id, b.lib.Standard.ID, b.lib.Translation, b.lib.Review)
				}
				return b.templateSteps(id, b.lib.HumanAI.ID, b.lib.Translation, b.lib.AIReview)
			}, nil},
	} {
		sp, err := b.q.CreateSettingsProfile(b.ctx, db.CreateSettingsProfileParams{TenantID: b.tenantID, FuzzyThreshold: 0.75, MtOnUpload: true, TmOnUpload: true})
		if err != nil {
			return fmt.Errorf("template settings: %w", err)
		}
		var tb *db.TermBasis
		if t.termBase {
			tb = &db.TermBasis{ID: bt.termBase}
		}
		if err := b.wireProfile(sp.ID, t.tm, tb, &bt.qaProfile, engines, t.targets); err != nil {
			return err
		}
		if err := t.workflow(sp.ID); err != nil {
			return err
		}
		customer := bt.customers[t.customer]
		src := "en"
		tmpl, err := b.q.CreateTemplate(b.ctx, db.CreateTemplateParams{
			Name: t.name, TenantID: b.tenantID, SettingsProfileID: sp.ID, ProjectName: &t.project,
			SourceLanguage: &src, TargetLanguages: t.targets, CustomerID: &customer.id, Domain: &t.domain,
		})
		if err != nil {
			return fmt.Errorf("template %q: %w", t.name, err)
		}
		picks, err := b.projectLabels(customer, t.domain)
		if err != nil {
			return err
		}
		for _, pick := range picks[:2] {
			if err := b.q.SetTemplateLabel(b.ctx, db.SetTemplateLabelParams{TemplateID: tmpl.ID, LabelDefinitionID: pick.defID, LabelValueID: pick.valueID}); err != nil {
				return fmt.Errorf("template %q label: %w", t.name, err)
			}
		}
		if t.applyTo != nil {
			if _, err := b.q.SetProjectTemplate(b.ctx, db.SetProjectTemplateParams{ID: t.applyTo.ID, TemplateID: &tmpl.ID, TenantID: b.tenantID}); err != nil {
				return fmt.Errorf("project template: %w", err)
			}
		}
	}
	return nil
}

func (b *builder) templateSteps(profileID, workflowID int64, defs ...db.StepDefinition) error {
	specs := make([]stepSpec, len(defs))
	for i := range defs {
		specs[i] = stepSpec{name: defs[i].Name, stepType: defs[i].StepType, def: &defs[i]}
	}
	_, err := b.steps(profileID, specs, &workflowID)
	return err
}

func (b *builder) tmProfile() error {
	threshold := float32(0.80)
	_, err := tmprofile.Create(b.ctx, b.q, b.tenantID, auth.UserIDPtr(b.ownerID), tmProfileName, tmProfileDesc,
		tmprofile.Scoring{FuzzyThreshold: &threshold},
		[]settings.TMAssignmentInput{{TmID: b.built.tmNordwind, Mode: "read_write", Priority: 0}})
	if err != nil {
		return fmt.Errorf("tm profile: %w", err)
	}
	return nil
}

func (b *builder) dashboards() error {
	if _, _, err := EnsureOperationsDashboard(b.ctx, b.q, b.tenantID, &b.ownerID); err != nil {
		return err
	}
	_, err := EnsurePersonalDashboard(b.ctx, b.q, b.tenantID, b.ownerID)
	return err
}

// analyses saves one finished analysis on each live project, through the same
// create and run paths the Analysis tab uses.
func (b *builder) analyses() error {
	svc := analysis.NewService(b.q, txOnBuilder{b})
	for _, a := range []struct {
		project db.Project
		name    string
		jobs    []int64
	}{
		{b.built.booking, analysisBooking, b.projectJobs(b.built.booking.ID)},
		{b.built.ifu, analysisIFU, b.projectJobs(b.built.ifu.ID)},
	} {
		row, err := analysis.Create(b.ctx, b.q, nil, b.tenantID, b.ownerID, analysis.CreateInput{ProjectID: a.project.ID, Name: a.name, JobIDs: a.jobs})
		if err != nil {
			return fmt.Errorf("analysis %q: %w", a.name, err)
		}
		if err := svc.Run(b.ctx, row.ID, b.tenantID); err != nil {
			return fmt.Errorf("run analysis %q: %w", a.name, err)
		}
	}
	return nil
}

func (b *builder) projectJobs(projectID int64) []int64 {
	for _, p := range b.res.Projects {
		if p.ID == projectID {
			return p.JobIDs
		}
	}
	return nil
}

// txOnBuilder runs the analysis worker's transaction on the builder's own.
type txOnBuilder struct{ b *builder }

func (t txOnBuilder) InTx(_ context.Context, fn func(db.Querier) error) error { return fn(t.b.q) }

// aiUsage writes a quarter of token usage for the AI engines, one row every
// three days, so the AI dashboard's cost chart has a curve.
func (b *builder) aiUsage() error {
	for _, e := range []struct {
		id             *int64
		name, provider string
		model, purpose string
		project        db.Project
		input, output  int64
	}{
		{b.ai.nordwind, aiEngineName, "openai", aiEngineModel, "qe_ape", b.built.booking, 42_000, 9_000},
		{b.ai.helix, aiClaudeSystem, "claude", aiClaudeModel, "qe_ape", b.built.ifu, 18_000, 4_000},
		{b.ai.translation, aiMTEngineName, "openai", aiEngineModel, "mt", b.built.booking, 26_000, 21_000},
	} {
		if e.id == nil {
			continue
		}
		for d := 1; d <= 90; d += 3 {
			scale := int64(1 + d%7)
			if err := b.exec(`INSERT INTO ai_usage_events (tenant_id, ai_engine_id, ai_engine_name, purpose, provider, model, project_id, input_tokens, output_tokens, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				b.tenantID, *e.id, e.name, e.purpose, e.provider, e.model, e.project.ID, e.input*scale/4, e.output*scale/4, b.daysAgo(d)); err != nil {
				return err
			}
		}
	}
	return nil
}
