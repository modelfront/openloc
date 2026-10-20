package translationmemory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
	"openloc/internal/translationmemory"
)

// Find-and-replace statements are planned with their values on every run,
// never with Postgres' generic plan: the generic plan cannot use the trigram
// indexes, and where most TMs are small it looks cheap, so a prepared
// statement switches to it after a few runs and then reads a large TM in
// full.
func TestIntegrationTMFindReplace_NeverUsesGenericPlans(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "FRPlanCorp")
	for i := 0; i < 400; i++ {
		small := testutil.SeedTM(t, q, tenant.ID, "small TM")
		testutil.SeedTMEntriesBulk(t, pool, small.ID, "en", "es", 5, func(i int) (string, string) {
			return fmt.Sprintf("source %d", i), fmt.Sprintf("destino %d", i)
		})
	}
	big := testutil.SeedTM(t, q, tenant.ID, "big TM")
	testutil.SeedTMEntriesBulk(t, pool, big.ID, "en", "es", 50000, func(i int) (string, string) {
		if i%500 == 0 {
			return fmt.Sprintf("source line %d", i), fmt.Sprintf("línea con Zanahoria %d", i)
		}
		return fmt.Sprintf("source line %d", i), fmt.Sprintf("línea destino %d", i)
	})
	testutil.Exec(t, pool, `ANALYZE translation_memory`)

	// One connection, so every run reuses the same prepared statements.
	cfg := pool.Config()
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	svc := translationmemory.NewService(db.New(one), one, one)
	fq := translationmemory.FindReplaceQuery{TMID: big.ID, Find: "zanahoria", Replace: "x", Scope: "target"}
	for i := 0; i < 12; i++ {
		p, err := svc.PreviewFindReplace(ctx, fq, 0, 50)
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		if p.TotalEntries != 100 {
			t.Fatalf("preview counted %d entries, want 100", p.TotalEntries)
		}
	}
	var generic, custom int64
	if err := one.QueryRow(ctx, `
		SELECT coalesce(sum(generic_plans), 0), coalesce(sum(custom_plans), 0) FROM pg_prepared_statements
		WHERE (statement LIKE '%-- name: FindTMEntriesForFR%' OR statement LIKE '%-- name: CountTMFRMatches%')
		  AND statement NOT LIKE '%pg_prepared_statements%'`).Scan(&generic, &custom); err != nil {
		t.Fatal(err)
	}
	if generic != 0 || custom < 24 {
		t.Errorf("12 previews ran %d generic and %d custom plans, want 0 generic and 24 custom", generic, custom)
	}
}
