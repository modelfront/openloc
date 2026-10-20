package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requiredTables lists tables the application requires to function.
// If any are missing, the server should refuse to start.
var requiredTables = []string{
	"tenants",
	"users",
	"projects",
	"files",
	"segments",
	"translations",
	"settings_profiles",
	"settings_tm_assignments",
	"mt_routers",
	"settings_terminology_assignments",
	"workflow_steps",
	"tm_collections",
	"term_concept",
	"term",
	"notifications",
	"qa_profiles",
}

// VerifySchema checks that all required tables exist in the database.
// Returns nil if all tables exist, or an error listing the missing ones.
func VerifySchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT tablename FROM pg_tables WHERE schemaname = 'public'
	`)
	if err != nil {
		return fmt.Errorf("failed to query table list: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("failed to scan table name: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading table list: %w", err)
	}

	var missing []string
	for _, t := range requiredTables {
		if !existing[t] {
			missing = append(missing, t)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required tables: %s — run migrations before starting the server", strings.Join(missing, ", "))
	}
	return nil
}

// DeepHealthCheck verifies that the connection is alive, all required tables
// exist and every embedded migration is applied. Returns nil if healthy, or an
// error describing the issue.
func DeepHealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	if err := VerifySchema(ctx, pool); err != nil {
		return err
	}

	return VerifyMigrated(ctx, pool)
}
