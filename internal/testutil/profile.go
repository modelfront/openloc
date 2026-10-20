package testutil

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// SetProfileMTRouter points a settings profile at an MT router, or at none
// when routerID is nil. It checks no tenant: a test may pair a profile with a
// router on purpose.
func SetProfileMTRouter(t testing.TB, q db.Querier, profileID int64, routerID *int64) {
	t.Helper()
	if err := q.SetSettingsProfileMTRouter(context.Background(), db.SetSettingsProfileMTRouterParams{ID: profileID, MtRouterID: routerID}); err != nil {
		t.Fatalf("SetProfileMTRouter %d: %v", profileID, err)
	}
}

// SeedTMAssignment assigns a translation memory to a settings profile. mode is
// "read", "write" or "read_write"; a lower priority is looked up first.
func SeedTMAssignment(t testing.TB, q db.Querier, profileID, tmID int64, mode string, priority int32) {
	t.Helper()
	if _, err := q.CreateSettingsTMAssignment(context.Background(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: profileID, TmID: tmID, Mode: mode, Priority: priority,
	}); err != nil {
		t.Fatalf("SeedTMAssignment TM %d on profile %d: %v", tmID, profileID, err)
	}
}

// SeedTMEntry stores one source/target pair in a translation memory.
func SeedTMEntry(t testing.TB, q db.Querier, tmID int64, sourceLang, targetLang, source, target string) db.TranslationMemory {
	t.Helper()
	entry, err := q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tmID, SourceLanguage: sourceLang, TargetLanguage: targetLang,
		SourceText: source, TargetText: target, CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("SeedTMEntry %q in TM %d: %v", source, tmID, err)
	}
	return entry
}
