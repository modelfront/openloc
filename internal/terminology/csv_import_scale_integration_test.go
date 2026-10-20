package terminology_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

// A large CSV imports in a bounded number of transactions, not one per row,
// and still skips each row that cannot become a concept on its own: an empty
// target, a row the database refuses (a NUL byte), while its neighbours land.
func TestIntegrationImportCSV_BatchesRowsAndSkipsBadOnes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "CSVScale")
	user := testutil.SeedUser(t, q, tenant.ID, "csv-scale@test.com", "pass")

	const rows = 1000
	var csv strings.Builder
	csv.WriteString("source_term,target_term\n")
	for i := 1; i <= rows; i++ {
		switch i {
		case 10:
			csv.WriteString("lonely,\n")
		case 500:
			fmt.Fprintf(&csv, "bad%d,nul\x00byte\n", i)
		default:
			fmt.Fprintf(&csv, "term %d,término %d\n", i, i)
		}
	}

	cpool, log := testutil.CountingPool(t)
	router := testutil.NewTestRouter(cpool)
	baseID := createCSVBase(t, router, "Scale", user.ID, tenant.ID)

	log.Reset()
	imported, skipped := importCSV(t, router, baseID, csv.String(), user.ID, tenant.ID)
	if imported != rows-2 || skipped != 2 {
		t.Errorf("imported %d, skipped %d; want %d and 2", imported, skipped, rows-2)
	}
	// Five 200-row batches, one of which falls back to a transaction per row.
	if n := log.CountPrefix("BEGIN"); n > 250 {
		t.Errorf("%d transactions for %d rows, want one per batch", n, rows)
	}

	var concepts int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM term_concept WHERE term_base_id = $1`, baseID).Scan(&concepts); err != nil {
		t.Fatal(err)
	}
	if concepts != rows-2 {
		t.Errorf("%d concepts stored, want %d", concepts, rows-2)
	}
}
