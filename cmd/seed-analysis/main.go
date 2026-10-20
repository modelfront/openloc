// Command seed-analysis creates the "Analysis export demo" project: five files
// × five target languages with one completed saved analysis whose numbers look
// like a real project's, for trying the Analyses tab and its CSV/XLSX download.
//
//	make seed-analysis   (also run by `make reset`)
//
// The analysis is written directly, not computed by the worker: each file has
// a fixed source word count, and the leverage differs by language the way a
// TM does — German and French have years of matches, Japanese is mostly new.
//
// Idempotent: the project (and its URL) is kept; its files, jobs and analyses
// are rebuilt, so analyses made while testing are thrown away.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/config"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/devseed"
)

const (
	projectName  = "Analysis export demo"
	analysisName = "Q4 quote — all files"
)

var targetLangs = []string{"de", "fr", "es", "it", "ja"}

// One scenario per target language: every job is waiting for its translator.
var translating = []devseed.Scenario{devseed.Translating, devseed.Translating, devseed.Translating, devseed.Translating, devseed.Translating}

var files = []devseed.File{
	{Name: "product-manual.txt", Segments: devseed.Sentences(0, 10), Scenarios: translating},
	{Name: "release-notes-4.2.txt", Segments: devseed.Sentences(11, 6), Scenarios: translating},
	{Name: "ui-strings.txt", Segments: devseed.Sentences(2, 8), Scenarios: translating},
	{Name: "marketing-brochure.txt", Segments: devseed.Sentences(5, 5), Scenarios: translating},
	{Name: "support-faq.txt", Segments: devseed.Sentences(8, 9), Scenarios: translating},
}

func main() {
	res := devseed.Run(devseed.Spec{ProjectName: projectName, TargetLangs: targetLangs, Steps: devseed.FourSteps, Files: files})

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, config.Load().DatabaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	analysisID, rows, err := seedAnalysis(ctx, pool, res.TenantID, res.ProjectID)
	if err != nil {
		log.Fatalf("seed analysis: %v", err)
	}

	fmt.Printf(`
✓ Project %q ready — organization %q (tenant %d)
  %d files × %d target languages (%v), analysis %q with %d rows

  Sign in to the %q organization, then open:
    %s/projects/%d/analyses   (analysis %d)
`, projectName, res.TenantName, res.TenantID, len(files), len(targetLangs), targetLangs, analysisName, rows,
		res.TenantName, devseed.BaseURL(), res.ProjectID, analysisID)
}

// seedAnalysis replaces the project's analyses with one completed analysis
// over every job.
func seedAnalysis(ctx context.Context, pool *pgxpool.Pool, tenantID, projectID int64) (int64, int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	q := db.New(tx)

	// -- Internal: dev seed cleanup of its own project; cascades to job_analysis.
	if _, err := tx.Exec(ctx, `DELETE FROM analyses WHERE project_id = $1 AND tenant_id = $2`, projectID, tenantID); err != nil {
		return 0, 0, fmt.Errorf("clear analyses: %w", err)
	}
	jobs, err := projectJobs(ctx, tx, projectID)
	if err != nil {
		return 0, 0, fmt.Errorf("list jobs: %w", err)
	}
	var owner int64
	if err := tx.QueryRow(ctx,
		`SELECT user_id FROM memberships WHERE tenant_id = $1 AND role = 'owner' ORDER BY user_id LIMIT 1`, tenantID).Scan(&owner); err != nil {
		return 0, 0, fmt.Errorf("find owner: %w", err)
	}

	ids := make([]int64, len(jobs))
	for i, j := range jobs {
		ids[i] = j.id
	}
	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID: tenantID, ProjectID: projectID, Name: analysisName, SourceLanguage: "en",
		JobIds: ids, CreatedBy: &owner, SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("insert analysis: %w", err)
	}
	for _, j := range jobs {
		p := breakdown(j.file, j.lang)
		p.AnalysisID, p.JobID = a.ID, &j.id
		if err := q.InsertJobAnalysis(ctx, p); err != nil {
			return 0, 0, fmt.Errorf("insert breakdown %s/%s: %w", j.file, j.lang, err)
		}
	}
	created := time.Now().Add(-2 * time.Hour)
	if _, err := tx.Exec(ctx,
		`UPDATE analyses SET status = 'completed', created_at = $2, completed_at = $3 WHERE id = $1`,
		a.ID, created, created.Add(47*time.Second)); err != nil {
		return 0, 0, fmt.Errorf("complete analysis: %w", err)
	}
	return a.ID, len(jobs), tx.Commit(ctx)
}

type job struct {
	id         int64
	file, lang string
}

// projectJobs is every job of the project, by file name then language.
func projectJobs(ctx context.Context, tx pgx.Tx, projectID int64) ([]job, error) {
	rows, err := tx.Query(ctx,
		`SELECT j.id, f.name, j.target_language FROM jobs j JOIN files f ON f.id = j.file_id
		 WHERE f.project_id = $1 ORDER BY f.name, j.target_language`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.file, &j.lang); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
