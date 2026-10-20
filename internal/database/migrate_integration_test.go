package database_test

import (
	"context"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"openloc/internal/database"
)

// sqlMigrationsOnDisk lists the migration files in the repo, which is what
// `make migrate-up` applies.
func sqlMigrationsOnDisk(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(migrationsDir())
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestMigrationsFSHoldsEveryMigration(t *testing.T) {
	onDisk := sqlMigrationsOnDisk(t)
	embedded, err := fs.Glob(database.MigrationsFS(), "*.sql")
	if err != nil {
		t.Fatalf("glob embedded: %v", err)
	}
	if len(embedded) != len(onDisk) || len(onDisk) == 0 {
		t.Fatalf("binary embeds %d migrations, repo has %d", len(embedded), len(onDisk))
	}
}

// `server migrate` on an empty database leaves it in the state the server
// checks at startup, and a second run is a no-op.
func TestIntegrationMigrateEmptyDatabase(t *testing.T) {
	sqlDB, connStr := setupDBWithURL(t)
	ctx := context.Background()
	onDisk := sqlMigrationsOnDisk(t)

	applied, err := database.Migrate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if applied != len(onDisk) {
		t.Fatalf("applied %d migrations, want all %d", applied, len(onDisk))
	}

	pool, err := database.New(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := database.VerifySchema(ctx, pool); err != nil {
		t.Fatalf("VerifySchema after Migrate: %v", err)
	}
	if err := database.DeepHealthCheck(ctx, pool); err != nil {
		t.Fatalf("DeepHealthCheck after Migrate: %v", err)
	}

	var newest int64
	for _, name := range onDisk {
		v, err := strconv.ParseInt(strings.SplitN(name, "_", 2)[0], 10, 64)
		if err == nil && v > newest {
			newest = v
		}
	}
	var recorded int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied").Scan(&recorded); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if recorded != newest {
		t.Fatalf("goose records version %d, newest migration is %d", recorded, newest)
	}

	again, err := database.Migrate(ctx, sqlDB)
	if err != nil || again != 0 {
		t.Fatalf("second Migrate: applied %d, err %v; want 0, nil", again, err)
	}
}

// A migration that is older than the newest applied one still runs: two
// branches can merge their timestamped migrations in either order.
func TestIntegrationMigrateAppliesOlderMissingMigration(t *testing.T) {
	sqlDB := setupDB(t)
	ctx := context.Background()

	tableMigration := func(table string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE " + table + " (id int);\n" +
			"-- +goose Down\nDROP TABLE " + table + ";\n")}
	}
	newer := fstest.MapFS{"20260102000000_newer.sql": tableMigration("ooo_newer")}
	if n, err := database.MigrateFS(ctx, sqlDB, newer); err != nil || n != 1 {
		t.Fatalf("apply the newer migration: applied %d, err %v", n, err)
	}

	both := fstest.MapFS{
		"20260101000000_older.sql": tableMigration("ooo_older"),
		"20260102000000_newer.sql": newer["20260102000000_newer.sql"],
	}
	n, err := database.MigrateFS(ctx, sqlDB, both)
	if err != nil || n != 1 {
		t.Fatalf("apply the older migration after the newer one: applied %d, err %v", n, err)
	}
	var exists bool
	if err := sqlDB.QueryRowContext(ctx, "SELECT to_regclass('ooo_older') IS NOT NULL").Scan(&exists); err != nil || !exists {
		t.Fatalf("older migration's table missing: exists=%v err=%v", exists, err)
	}
}

// A database behind the binary is unhealthy, whichever migration it lacks: the
// newest one, or an older one applied out of order (`up -allow-missing`),
// which the highest recorded version does not reveal.
func TestIntegrationDeepHealthCheckFailsWhileAMigrationIsPending(t *testing.T) {
	sqlDB, connStr := setupDBWithURL(t)
	ctx := context.Background()
	if _, err := database.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := database.New(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := database.DeepHealthCheck(ctx, pool); err != nil {
		t.Fatalf("DeepHealthCheck on a migrated database: %v", err)
	}

	onDisk := sqlMigrationsOnDisk(t)
	for _, name := range []string{onDisk[len(onDisk)-1], onDisk[len(onDisk)/2]} {
		version, err := strconv.ParseInt(strings.SplitN(name, "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatalf("version of %s: %v", name, err)
		}
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id = $1", version); err != nil {
			t.Fatalf("unrecord %s: %v", name, err)
		}
		err = database.DeepHealthCheck(ctx, pool)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("DeepHealthCheck with %s pending = %v, want an error naming it", name, err)
		}
		if _, err := sqlDB.ExecContext(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", version); err != nil {
			t.Fatalf("re-record %s: %v", name, err)
		}
	}
}
