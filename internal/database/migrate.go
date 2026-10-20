package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The migrations ship inside the binary so `server migrate` works from the
// image alone. The embed lives here, not in migrations/: the goose CLI
// (`make migrate-up`) reads that folder and must find only SQL files there.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// MigrationsFS is the embedded migrations folder, rooted at the SQL files.
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		panic(err) // the embed pattern above guarantees the folder exists
	}
	return sub
}

// Migrate applies every pending migration embedded in the binary and returns
// how many it applied. Out-of-order versions are allowed, as with
// `make migrate-up -allow-missing`: timestamped migrations from different
// branches merge in any order.
func Migrate(ctx context.Context, sqlDB *sql.DB) (int, error) {
	return migrate(ctx, sqlDB, MigrationsFS())
}

func migrate(ctx context.Context, sqlDB *sql.DB, migrations fs.FS) (int, error) {
	provider, err := newProvider(sqlDB, migrations)
	if err != nil {
		return 0, err
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return len(results), fmt.Errorf("apply migrations: %w", err)
	}
	return len(results), nil
}

// VerifyMigrated returns an error naming every embedded migration the database
// has not applied. Out-of-order versions count, as with `up -allow-missing`:
// an older migration merged after a newer one is pending even though the
// highest recorded version is current. Call it after VerifySchema, which
// reports an empty database more plainly.
func VerifyMigrated(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := newProvider(sqlDB, MigrationsFS())
	if err != nil {
		return err
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check pending migrations: %w", err)
	}
	if !pending {
		return nil
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("list pending migrations: %w", err)
	}
	var names []string
	for _, s := range statuses {
		if s.State == goose.StatePending {
			names = append(names, path.Base(s.Source.Path))
		}
	}
	return fmt.Errorf("database is missing %d migration(s): %s — run migrations before starting the server",
		len(names), strings.Join(names, ", "))
}

// newProvider is the one goose setup Migrate and VerifyMigrated share, so the
// check counts as pending exactly what a migrate run would apply.
func newProvider(sqlDB *sql.DB, migrations fs.FS) (*goose.Provider, error) {
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithAllowOutofOrder(true))
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return provider, nil
}
