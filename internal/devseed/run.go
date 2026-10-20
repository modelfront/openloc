package devseed

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/config"
	"openloc/internal/demodata"
)

// Run is a seeder command's body: it connects to the configured database,
// seeds spec's project, and exits the process on any error.
func Run(spec Spec) Result {
	cfg := config.Load()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	res, err := Seed(ctx, pool, spec)
	if err != nil {
		log.Fatalf("seed %q: %v", spec.ProjectName, err)
	}
	// The rows are inserted directly: give them the origin event pre-translation
	// writes and date them by their first event, as `make seed` does.
	if err := demodata.HealOriginHistory(ctx, pool, res.TenantID); err != nil {
		log.Fatalf("seed %q: %v", spec.ProjectName, err)
	}
	if err := demodata.AlignTranslationDates(ctx, pool, res.TenantID); err != nil {
		log.Fatalf("seed %q: %v", spec.ProjectName, err)
	}
	return res
}
