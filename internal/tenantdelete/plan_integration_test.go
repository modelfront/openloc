package tenantdelete_test

import (
	"context"
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Each delete step reads only the deleted tenant's rows, however many files,
// TM entries and term concepts another tenant holds with lower ids.
func TestIntegrationDeleteTenant_BatchesReadOnlyTheTenantsRows(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// The big tenant: 200 projects, TMs and term bases of 100 rows each, half
	// of the concepts soft-deleted.
	_, bigID := testutil.RegisterUser(t, q, "big")
	for i := 0; i < 200; i++ {
		project := testutil.SeedProject(t, q, bigID, fmt.Sprintf("big project %d", i), "en", []string{"de"})
		exec(`
			INSERT INTO files (project_id, name, format, storage_path)
			SELECT $1, 'f' || g || '.txt', 'txt', 'test/f' || g FROM generate_series(1, 100) g`, project.ID)
		tm := testutil.SeedTM(t, q, bigID, fmt.Sprintf("big TM %d", i))
		testutil.SeedTMEntriesBulk(t, pool, tm.ID, "en", "de", 100, func(i int) (string, string) {
			return fmt.Sprintf("source %d", i), fmt.Sprintf("Ziel %d", i)
		})
		tb := testutil.SeedTermBase(t, q, bigID, fmt.Sprintf("big terms %d", i), "en", "de")
		exec(`
			INSERT INTO term_concept (term_base_id, deleted_at)
			SELECT $1, CASE WHEN g % 2 = 0 THEN now() END FROM generate_series(1, 100) g`, tb.ID)
	}

	_, smallID := testutil.RegisterUser(t, q, "small")
	smallProject := testutil.SeedProject(t, q, smallID, "small project", "en", []string{"de"})
	testutil.SeedFile(t, q, smallProject.ID, "small.txt", db.FileFormatTxt)
	smallTM := testutil.SeedTM(t, q, smallID, "small TM")
	testutil.SeedTMEntriesBulk(t, pool, smallTM.ID, "en", "de", 1, func(int) (string, string) { return "source", "Ziel" })
	smallTB := testutil.SeedTermBase(t, q, smallID, "small terms", "en", "de")
	exec(`INSERT INTO term_concept (term_base_id) VALUES ($1)`, smallTB.ID)
	exec(`ANALYZE files; ANALYZE projects; ANALYZE translation_memory; ANALYZE tm_collections; ANALYZE term_concept; ANALYZE term_bases`)

	for _, c := range []struct {
		query, table string
		arg          int64
	}{
		{"DeleteProjectFileBatch", "files", smallProject.ID},
		{"DeleteTenantTMEntryBatch", "translation_memory", smallID},
		{"DeleteTenantTermConceptBatch", "term_concept", smallID},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan := testutil.ExplainAnalyze(t, tx, testutil.GeneratedSQL(t, c.query), c.arg)
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if read := plan.RowsScanned(c.table); read > 10 {
			t.Errorf("%s read %.0f rows of %s to delete the small tenant's one row, want at most 10", c.query, read, c.table)
		}
	}
}
