package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
	tmpl "openloc/internal/template"
	"openloc/internal/wordcount"
)

// The plain-text demo: a template with "Strings contain HTML" off, and an
// en→es project made from it whose JSON file was read as plain text, so its
// strings show their tags as text and the editor has no tag chips.
//
// It lives in the second org, like the History demo: the main org's seed
// steps take its newest template as the Default Template.
const (
	plainTextTemplateName = "Plain-text strings"
	plainTextProjectName  = "Plain-text strings demo"
	plainTextFileName     = "plain-strings.json"
)

var plainTextSegments = []struct{ key, source string }{
	{"help.bold", "Use <b> for bold text."},
	{"help.code", "Put code between <code> and </code>."},
	{"help.enter", "Press <Enter> to continue."},
	{"welcome", "Welcome back!"},
}

// healPlainTextDemo creates the template and the project in the "acme" org,
// each unless one by its name is already there.
func healPlainTextDemo(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, userID int64) {
	tenant, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		log.Printf("⚠ Plain-text demo skipped, no second org: %v", err)
		return
	}
	template, err := healPlainTextTemplate(ctx, q, pool, tenant.ID)
	if err != nil {
		log.Printf("⚠ Plain-text demo template: %v", err)
		return
	}
	projects, err := q.ListProjects(ctx, tenant.ID)
	if err != nil {
		log.Printf("⚠ Plain-text demo: list projects: %v", err)
		return
	}
	for _, p := range projects {
		if p.Name == plainTextProjectName {
			log.Printf("✓ Plain-text demo already present (%s, project %d)", tenant.Name, p.ID)
			return
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("⚠ Plain-text demo: begin: %v", err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	projectID, err := createPlainTextProject(ctx, q.WithTx(tx), tx, tenant.ID, userID, template)
	if err != nil {
		log.Printf("⚠ Plain-text demo not created: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("⚠ Plain-text demo: commit: %v", err)
		return
	}
	log.Printf("✓ Plain-text demo created in %s (project %d, template %q)", tenant.Name, projectID, template.Name)
}

func healPlainTextTemplate(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) (db.Template, error) {
	templates, err := q.ListTemplates(ctx, tenantID)
	if err != nil {
		return db.Template{}, fmt.Errorf("list templates: %w", err)
	}
	for _, t := range templates {
		if t.Name == plainTextTemplateName {
			return t, nil
		}
	}
	off := false
	source := "en"
	t, _, err := tmpl.Create(ctx, q, pool, tenantID, tmpl.CreateInput{
		Name: plainTextTemplateName, SourceLanguage: &source, TargetLanguages: []string{"es"},
		Setup: settings.Setup{InlineHTML: &off},
	})
	if err != nil {
		return db.Template{}, fmt.Errorf("create template: %w", err)
	}
	return t, nil
}

func createPlainTextProject(ctx context.Context, q *db.Queries, tx pgx.Tx, tenantID, userID int64, template db.Template) (int64, error) {
	source, target := "en", "es"
	tmplProfile, err := q.GetSettingsProfile(ctx, template.SettingsProfileID)
	if err != nil {
		return 0, fmt.Errorf("get template profile: %w", err)
	}
	profile, err := settings.CreateProfileLike(ctx, q, tenantID, tmplProfile)
	if err != nil {
		return 0, err
	}
	if err := settings.CreateDefaultWorkflowSteps(ctx, q, tenantID, profile.ID); err != nil {
		return 0, fmt.Errorf("create workflow steps: %w", err)
	}
	steps, err := q.ListSettingsWorkflowSteps(ctx, profile.ID)
	if err != nil || len(steps) == 0 {
		return 0, fmt.Errorf("list workflow steps (%d): %v", len(steps), err)
	}
	project, err := q.CreateProject(ctx, db.CreateProjectParams{
		Name: plainTextProjectName, SourceLanguage: source, TargetLanguages: []string{target},
		TenantID: tenantID, SettingsProfileID: profile.ID,
		Status: db.ProjectStatusInProgress, CreatedBy: auth.UserIDPtr(userID),
	})
	if err != nil {
		return 0, fmt.Errorf("create project: %w", err)
	}
	if _, err := q.SetProjectTemplate(ctx, db.SetProjectTemplateParams{
		ID: project.ID, TemplateID: &template.ID, TenantID: tenantID,
	}); err != nil {
		return 0, fmt.Errorf("link template: %w", err)
	}

	// storage_path is a placeholder — the file is not S3-exportable.
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ProjectID: project.ID, Name: plainTextFileName, Format: db.FileFormatJsonI18n,
		StoragePath:  fmt.Sprintf("seed/plain-strings-%d.json", project.ID),
		SegmentCount: int32(len(plainTextSegments)),
		InlineHtml:   &profile.InlineHtml,
	})
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	words := 0
	for pos, s := range plainTextSegments {
		wc := wordcount.CountWords(s.source)
		words += wc
		// No source_rich_text: the file is plain text, so no tag chips.
		var segID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO segments (file_id, source_text, segment_key, position, word_count) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			file.ID, s.source, s.key, pos, wc,
		).Scan(&segID); err != nil {
			return 0, fmt.Errorf("insert segment %q: %w", s.key, err)
		}
		if _, err := q.CreateTranslation(ctx, db.CreateTranslationParams{
			SegmentID: segID, TargetLanguage: target, Status: db.TranslationStatusNeedsReview,
		}); err != nil {
			return 0, fmt.Errorf("insert translation %q: %w", s.key, err)
		}
	}
	job, err := q.CreateJob(ctx, db.CreateJobParams{FileID: file.ID, TargetLanguage: target, WordCount: int32(words)})
	if err != nil {
		return 0, fmt.Errorf("create job: %w", err)
	}
	if _, err := startDemoJob(ctx, q, job.ID, steps, userID); err != nil {
		return 0, err
	}
	return project.ID, nil
}
