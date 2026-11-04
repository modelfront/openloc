package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
)

// The filter profile demo: an Excel profile that translates column B under a
// header row, so the Filter profiles page shows an org profile next to the
// built-ins, and an upload can pick it.
const demoFilterProfileName = "Product sheet – column B"

func healFilterProfile(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	svc := filterprofile.NewService(q, pool)
	profiles, err := svc.List(ctx, tenantID, filterprofile.FormatXLSX)
	if err != nil {
		log.Printf("⚠ Could not list filter profiles: %v", err)
		return
	}
	for _, p := range profiles {
		if p.Name == demoFilterProfileName && !p.Builtin {
			log.Printf("✓ Filter profile: %s (ID: %d)", p.Name, p.ID)
			return
		}
	}
	profile, err := svc.Create(ctx, tenantID, 0, filterprofile.Input{
		Name: demoFilterProfileName, Format: filterprofile.FormatXLSX,
		Options: &filterprofile.Options{XLSX: &filterprofile.XLSXRules{HeaderRow: true, Columns: []string{"B"}}},
	})
	if err != nil {
		log.Printf("⚠ Failed to create the filter profile: %v", err)
		return
	}
	log.Printf("✓ Created filter profile %q", profile.Name)
}
