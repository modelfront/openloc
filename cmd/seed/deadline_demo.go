package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
)

// The deadline demo: one project without files per deadline band, so the
// project list's Deadline filter has something in every option. It lives in
// the second org, like the other demos, so the main org's projects stay as
// the tests expect them.
//
// Every run sets each deadline to the same distance from now, so the bands
// hold however old the seeded database is.
var deadlineDemoProjects = []struct {
	name   string
	status db.ProjectStatus
	offset string // a Postgres interval from now(); empty = no deadline
}{
	{"Deadline demo: overdue", db.ProjectStatusInProgress, "-2 days"},
	{"Deadline demo: overdue, on hold", db.ProjectStatusOnHold, "-6 hours"},
	{"Deadline demo: due today", db.ProjectStatusInProgress, "5 hours"},
	{"Deadline demo: due this week", db.ProjectStatusInProgress, "3 days"},
	{"Deadline demo: due later", db.ProjectStatusInProgress, "21 days"},
	{"Deadline demo: no deadline", db.ProjectStatusInProgress, ""},
	// Finished late: never overdue, so it is in no band.
	{"Deadline demo: completed late", db.ProjectStatusCompleted, "-3 days"},
}

// healDeadlineDemo creates the demo projects missing from the "acme" org and
// moves every one's deadline back to its distance from now.
func healDeadlineDemo(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, userID int64) {
	tenant, err := q.GetTenantBySlug(ctx, "acme")
	if err != nil {
		log.Printf("⚠ Deadline demo skipped, no second org: %v", err)
		return
	}
	projects, err := q.ListProjects(ctx, tenant.ID)
	if err != nil {
		log.Printf("⚠ Deadline demo: list projects: %v", err)
		return
	}
	existing := make(map[string]bool, len(projects))
	for _, p := range projects {
		existing[p.Name] = true
	}
	created := 0
	for _, d := range deadlineDemoProjects {
		if existing[d.name] {
			continue
		}
		if err := createDeadlineDemoProject(ctx, q, pool, tenant.ID, userID, d.name, d.status); err != nil {
			log.Printf("⚠ Deadline demo %q not created: %v", d.name, err)
			return
		}
		created++
	}
	for _, d := range deadlineDemoProjects {
		// NULLIF: an empty offset leaves the project without a deadline.
		if _, err := pool.Exec(ctx, `
			UPDATE projects SET deadline = now() + NULLIF($1, '')::interval
			WHERE tenant_id = $2 AND name = $3 AND deleted_at IS NULL`,
			d.offset, tenant.ID, d.name); err != nil {
			log.Printf("⚠ Deadline demo %q: set deadline: %v", d.name, err)
			return
		}
	}
	log.Printf("✓ Deadline demo in %s (%d project(s) created, %d dated)", tenant.Name, created, len(deadlineDemoProjects))
}

// createDeadlineDemoProject creates one empty en→de project with the default
// workflow steps.
func createDeadlineDemoProject(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID, userID int64, name string, status db.ProjectStatus) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	qtx := q.WithTx(tx)
	profile, err := settings.CreateProfileFor(ctx, qtx, db.CreateSettingsProfileParams{
		TenantID: tenantID, FuzzyThreshold: 0.75,
	}, "en")
	if err != nil {
		return err
	}
	if err := settings.CreateDefaultWorkflowSteps(ctx, qtx, tenantID, profile.ID); err != nil {
		return fmt.Errorf("create workflow steps: %w", err)
	}
	if _, err := qtx.CreateProject(ctx, db.CreateProjectParams{
		Name: name, SourceLanguage: "en", TargetLanguages: []string{"de"},
		TenantID: tenantID, SettingsProfileID: profile.ID,
		Status: status, CreatedBy: auth.UserIDPtr(userID),
	}); err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return tx.Commit(ctx)
}
