package settings_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// A settings row linked to a TM profile must resolve TM rows, write TMs,
// concordance hits and match scoring from the PROFILE's settings row; an
// unlinked row resolves its own. Every lookup is driven by the project's own
// settings_profile_id — callers never learn about the link.
func TestIntegrationTMProfile_LookupsResolveThroughLink(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ProfileCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "Linked", "en", []string{"fr"})
	ownTM := testutil.SeedTMWithLangs(t, q, tenant.ID, "Own TM", "en", []string{"fr"})
	profTM := testutil.SeedTMWithLangs(t, q, tenant.ID, "Profile TM", "en", []string{"fr"})

	// Project's own row: TM A, threshold 0.75 (SeedProject default).
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: ownTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign own TM: %v", err)
	}
	// Profile: TM B, threshold 0.5, penalty 7 on the TM row.
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Customer X", 0.5)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read_write", Priority: 1, PenaltyPct: 7,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	for _, tm := range []db.TmCollection{ownTM, profTM} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "fr",
			SourceText: "Hello world", TargetText: "Bonjour " + tm.Name, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
	}

	lookupParams := db.LookupSettingsTMsParams{SettingsProfileID: project.SettingsProfileID, SourceLanguage: "en", TargetLanguage: "fr"}
	writeParams := db.LookupSettingsWriteTMsParams{SettingsProfileID: project.SettingsProfileID, SourceLanguage: "en", TargetLanguage: "fr"}
	query := "Hello"
	concParams := db.SettingsConcordanceSearchParams{SettingsProfileID: project.SettingsProfileID, SourceLanguage: "en", TargetLanguage: "fr", Query: &query, EntryLimit: 10}

	// --- Unlinked: everything comes from the project's own row. ---
	rows, err := q.LookupSettingsTMs(ctx, lookupParams)
	if err != nil || len(rows) != 1 || rows[0].TmID != ownTM.ID {
		t.Fatalf("unlinked LookupSettingsTMs: want [own], got %+v err=%v", rows, err)
	}
	scope, err := q.GetTMScopeSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil || scope.ID != project.SettingsProfileID {
		t.Fatalf("unlinked scope: want own row %d, got %d err=%v", project.SettingsProfileID, scope.ID, err)
	}

	// --- Link. ---
	testutil.LinkTMProfile(t, q, project.SettingsProfileID, tp.ID)

	rows, err = q.LookupSettingsTMs(ctx, lookupParams)
	if err != nil {
		t.Fatalf("LookupSettingsTMs: %v", err)
	}
	if len(rows) != 1 || rows[0].TmID != profTM.ID {
		t.Fatalf("linked LookupSettingsTMs: want [profile TM %d], got %+v", profTM.ID, rows)
	}
	if rows[0].PenaltyPct != 7 {
		t.Errorf("per-TM penalty must come from the profile row: want 7, got %d", rows[0].PenaltyPct)
	}
	wrows, err := q.LookupSettingsWriteTMs(ctx, writeParams)
	if err != nil || len(wrows) != 1 || wrows[0] != profTM.ID {
		t.Fatalf("linked LookupSettingsWriteTMs: want [profile TM], got %v err=%v", wrows, err)
	}
	hits, err := q.SettingsConcordanceSearch(ctx, concParams)
	if err != nil || len(hits) != 1 || hits[0].TargetText != "Bonjour Profile TM" {
		t.Fatalf("linked concordance: want the profile TM's entry only, got %+v err=%v", hits, err)
	}
	scope, err = q.GetTMScopeSettingsProfile(ctx, project.SettingsProfileID)
	if err != nil {
		t.Fatalf("GetTMScopeSettingsProfile: %v", err)
	}
	if scope.ID != tp.SettingsProfileID || scope.FuzzyThreshold != 0.5 {
		t.Errorf("linked scope: want profile row %d @0.5, got %d @%v", tp.SettingsProfileID, scope.ID, scope.FuzzyThreshold)
	}

	// --- Editing the profile is live: the next lookup sees it. ---
	if err := q.DeleteSettingsTMAssignments(ctx, tp.SettingsProfileID); err != nil {
		t.Fatalf("clear profile TMs: %v", err)
	}
	rows, err = q.LookupSettingsTMs(ctx, lookupParams)
	if err != nil || len(rows) != 0 {
		t.Fatalf("after emptying the profile the project must see no TMs, got %+v err=%v", rows, err)
	}

	// --- Unlink: back to the own row (TM A is still there). ---
	if _, err := q.SetSettingsProfileTMProfile(ctx, db.SetSettingsProfileTMProfileParams{ID: project.SettingsProfileID}); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	rows, err = q.LookupSettingsTMs(ctx, lookupParams)
	if err != nil || len(rows) != 1 || rows[0].TmID != ownTM.ID {
		t.Fatalf("unlinked again: want [own], got %+v err=%v", rows, err)
	}
}

// Link clears the row's own TM assignments; detach copies the profile's TM rows
// and its three scoring values back and clears the link, so the project keeps
// translating the same way with no profile behind it.
func TestIntegrationTMProfile_LinkAndDetach(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "DetachCorp")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "P", "en", []string{"fr"})
	ownTM := testutil.SeedTM(t, q, tenant.ID, "Own")
	profTM := testutil.SeedTM(t, q, tenant.ID, "Prof")
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: ownTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign own TM: %v", err)
	}
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared", 0.6)
	sub, near := int32(9), int32(4)
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: tp.SettingsProfileID, FuzzyThreshold: 0.6, SublanguagePenaltyPct: &sub, NearExactPenaltyPct: &near,
	}); err != nil {
		t.Fatalf("configure profile scoring: %v", err)
	}
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profTM.ID, Mode: "read", Priority: 3, PenaltyPct: 12,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	foreign := testutil.SeedTMProfile(t, q, other.ID, "Foreign", 0.5)

	// Cross-tenant link is refused as a client error, and nothing changes.
	if _, err := settings.LinkTMProfileTx(ctx, pool, tenant.ID, project.SettingsProfileID, foreign.ID); err == nil {
		t.Fatal("linking another tenant's profile must fail")
	} else if _, ok := httputil.AsClientError(err); !ok {
		t.Fatalf("want a client error, got %v", err)
	}
	if rows, _ := q.ListSettingsTMAssignments(ctx, project.SettingsProfileID); len(rows) != 1 {
		t.Fatalf("a refused link must not touch own rows, got %d", len(rows))
	}

	// Detach while unlinked is a client error.
	if _, err := settings.DetachTMProfileTx(ctx, pool, project.SettingsProfileID); err == nil {
		t.Fatal("detaching an unlinked row must fail")
	}

	row, err := settings.LinkTMProfileTx(ctx, pool, tenant.ID, project.SettingsProfileID, tp.ID)
	if err != nil {
		t.Fatalf("LinkTMProfileTx: %v", err)
	}
	if row.TmProfileID == nil || *row.TmProfileID != tp.ID {
		t.Fatalf("link not stored: %v", row.TmProfileID)
	}
	if rows, _ := q.ListSettingsTMAssignments(ctx, project.SettingsProfileID); len(rows) != 0 {
		t.Errorf("link must clear own TM rows, got %d", len(rows))
	}

	row, err = settings.DetachTMProfileTx(ctx, pool, project.SettingsProfileID)
	if err != nil {
		t.Fatalf("DetachTMProfileTx: %v", err)
	}
	if row.TmProfileID != nil {
		t.Errorf("detach must clear the link, got %v", *row.TmProfileID)
	}
	if row.FuzzyThreshold != 0.6 || row.SublanguagePenaltyPct != 9 || row.NearExactPenaltyPct != 4 {
		t.Errorf("detach must copy scoring 0.6/9/4, got %v/%d/%d", row.FuzzyThreshold, row.SublanguagePenaltyPct, row.NearExactPenaltyPct)
	}
	rows, err := q.ListSettingsTMAssignments(ctx, project.SettingsProfileID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("detach must copy the profile's rows, got %d err=%v", len(rows), err)
	}
	if rows[0].TmID != profTM.ID || rows[0].Mode != "read" || rows[0].Priority != 3 || rows[0].PenaltyPct != 12 {
		t.Errorf("copied row = %+v, want profile TM read/3/12", rows[0])
	}
	// The profile itself is untouched.
	if prows, _ := q.ListSettingsTMAssignments(ctx, tp.SettingsProfileID); len(prows) != 1 {
		t.Errorf("detach must not modify the profile, got %d rows", len(prows))
	}
}
