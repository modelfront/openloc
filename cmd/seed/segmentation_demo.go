package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentationapi"
)

// The segmentation demo: an org copy of the English built-in with a few legal
// abbreviations, so the Segmentation profiles page shows an org profile next
// to the built-ins, and an English project can pick it.
const demoSegmentationName = "English – legal"

var demoLegalAbbreviations = []string{"Art", "Sec", "Sch", "Reg", "cl", "para", "subs"}

func healSegmentationProfile(ctx context.Context, q *db.Queries, pool *pgxpool.Pool, tenantID int64) {
	svc := segmentationapi.NewService(q, pool)
	profiles, err := svc.List(ctx, tenantID, "en")
	if err != nil {
		log.Printf("⚠ Could not list segmentation profiles: %v", err)
		return
	}
	var english *segmentationapi.Profile
	for i, p := range profiles {
		if p.Name == demoSegmentationName && !p.Builtin {
			log.Printf("✓ Segmentation profile: %s (ID: %d)", p.Name, p.ID)
			return
		}
		if p.BuiltinKey == "builtin:en@1" {
			english = &profiles[i]
		}
	}
	if english == nil {
		log.Printf("⚠ No English built-in segmentation profile; run the migrations")
		return
	}
	def := english.Definition
	def.Abbreviations = append(append([]string{}, def.Abbreviations...), demoLegalAbbreviations...)
	profile, err := svc.Create(ctx, tenantID, 0, segmentationapi.Input{
		Name: demoSegmentationName, SourceLanguages: []string{"en"}, Definition: &def, From: &english.ID,
	})
	if err != nil {
		log.Printf("⚠ Failed to create the segmentation profile: %v", err)
		return
	}
	log.Printf("✓ Created segmentation profile %q", profile.Name)
}
