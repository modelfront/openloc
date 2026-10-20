package main

import (
	"context"
	"log"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
)

// ensureMTRouter returns the tenant's MT router called name, creating it with
// mtEngineID as its default (no per-language rules) when missing.
func ensureMTRouter(ctx context.Context, q *db.Queries, tenantID int64, name string, mtEngineID int64) (db.MtRouter, bool) {
	r, created, err := demodata.EnsureMTRouter(ctx, q, tenantID, name, mtEngineID)
	if err != nil {
		log.Printf("⚠ Could not ensure MT router %q: %v", name, err)
		return db.MtRouter{}, false
	}
	if created {
		log.Printf("  ✓ MT router: %s", name)
	}
	return r, true
}

// pointProfileAtMTRouter sets a profile's MT router when it has none yet.
func pointProfileAtMTRouter(ctx context.Context, q *db.Queries, profileID, routerID int64) {
	profile, err := q.GetSettingsProfile(ctx, profileID)
	if err != nil || profile.MtRouterID != nil {
		return
	}
	if err := q.SetSettingsProfileMTRouter(ctx, db.SetSettingsProfileMTRouterParams{ID: profileID, MtRouterID: &routerID}); err != nil {
		log.Printf("⚠ Could not point profile %d at MT router %d: %v", profileID, routerID, err)
		return
	}
	log.Printf("  ✓ Profile %d → MT router %q", profileID, demodata.MTRouterName)
}
