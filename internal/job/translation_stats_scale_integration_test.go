package job_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationTranslationCountReaders_NeverReadTranslations pins that the
// job list counts, the single-job counts, the progress report and the
// stuck-job list read the per-job counts table, not every translation: each
// runs inside a transaction whose table statistics must show no rows read from
// translations and segments.
func TestIntegrationTranslationCountReaders_NeverReadTranslations(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ScaleCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Scale", "en", []string{"fr"})
	if _, err := pool.Exec(ctx, `UPDATE projects SET status = 'in_progress' WHERE id = $1`, project.ID); err != nil {
		t.Fatalf("project status: %v", err)
	}
	file := testutil.SeedFile(t, q, project.ID, "big.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")
	testutil.SeedSegmentsBulk(t, pool, file.ID, "fr", 0, 2000, "Texte")

	readers := map[string]func(db.Querier) error{
		"GetTranslationCountsByProject": func(tq db.Querier) error {
			_, err := tq.GetTranslationCountsByProject(ctx, db.GetTranslationCountsByProjectParams{ProjectID: project.ID})
			return err
		},
		"GetJobTranslationCounts": func(tq db.Querier) error {
			_, err := tq.GetJobTranslationCounts(ctx, db.GetJobTranslationCountsParams{FileID: job.FileID, TargetLanguage: job.TargetLanguage})
			return err
		},
		"GetTranslationProgress": func(tq db.Querier) error {
			_, err := tq.GetTranslationProgress(ctx, db.GetTranslationProgressParams{TenantID: tenant.ID, ProjectIds: []int64{project.ID}})
			return err
		},
		"ListStaleJobs": func(tq db.Querier) error {
			_, err := tq.ListStaleJobs(ctx, db.ListStaleJobsParams{
				TenantID: tenant.ID, RowLimit: 50,
				Before: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			})
			return err
		},
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			tables := []string{"translations", "segments"}
			before := map[string]int64{}
			for _, table := range tables {
				before[table] = testutil.TableRowsRead(t, tx, table)
			}
			if err := read(db.New(tx)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			for _, table := range tables {
				if n := testutil.TableRowsRead(t, tx, table) - before[table]; n != 0 {
					t.Errorf("%s rows read from %s: got %d, want 0", name, table, n)
				}
			}
		})
	}
}
