package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// A project linked to a TM profile must translate with the PROFILE's TMs and
// the PROFILE's threshold. Set up so both halves are load-bearing: the fuzzy
// entry lives only in the profile's TM, and its ~0.71 similarity sits below the
// project's own 0.75 threshold but above the profile's 0.6. Resolving either
// the TM list or the threshold from the project's own row sends the segment to
// MT instead.
func TestIntegrationTranslateFile_LinkedTMProfileDrivesMatching(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Own row: "Test TM" (empty), threshold 0.75.
	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Link to reset your password."}, 0.75)
	file, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetFileTenant: %v", err)
	}
	project, err := q.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}

	profileTM := testutil.SeedTM(t, q, tenantID, "Profile TM")
	tp := testutil.SeedTMProfile(t, q, tenantID, "Loose profile", 0.6)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profileTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: profileTM.ID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Reset your password.", TargetText: "Réinitialisez votre mot de passe.", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}
	testutil.LinkTMProfile(t, q, project.SettingsProfileID, tp.ID)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{FileID: fileID, TargetLanguage: "fr"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTranslationsByFile: rows=%d err=%v", len(rows), err)
	}
	tr := rows[0]
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Fatalf("expected origin 'tm' from the profile's TM at the profile's threshold, got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent < 60 || *tr.TmMatchPercent >= 75 {
		t.Errorf("expected a fuzzy in [60,75) — only the profile's 0.6 threshold admits it — got %v", tr.TmMatchPercent)
	}
	if tr.ActiveText == nil || *tr.ActiveText != "Réinitialisez votre mot de passe." {
		t.Errorf("expected the profile TM's target, got %v", tr.ActiveText)
	}
}

// Confirmed segments in a linked project are written back into the PROFILE's
// write TM, not into the project's own (now unused) TM rows.
func TestIntegrationTMWriteBack_LinkedProfileWriteTM(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "WriteCorp")
	ownTM := testutil.SeedTM(t, q, tenant.ID, "Own TM")
	profileTM := testutil.SeedTM(t, q, tenant.ID, "Profile TM")
	project := testutil.SeedProject(t, q, tenant.ID, "FR Project", "en", []string{"fr"})
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID, TmID: ownTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign own TM: %v", err)
	}
	tp := testutil.SeedTMProfile(t, q, tenant.ID, "Shared", 0.75)
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: tp.SettingsProfileID, TmID: profileTM.ID, Mode: "read_write", Priority: 1,
	}); err != nil {
		t.Fatalf("assign profile TM: %v", err)
	}
	testutil.LinkTMProfile(t, q, project.SettingsProfileID, tp.ID)

	file := testutil.SeedFile(t, q, project.ID, "test.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusApproved)
	human := "Bonjour le monde"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: tr.ID, HumanText: &human}); err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	result, err := svc.SaveApprovedToTM(ctx, tenant.ID, file.ID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if result.TMSavedCount != 1 || result.NoWriteTMs {
		t.Fatalf("expected 1 TM save via the profile's write TM, got saved=%d noWriteTMs=%v", result.TMSavedCount, result.NoWriteTMs)
	}
	if _, err := q.LookupTM(ctx, db.LookupTMParams{TmID: profileTM.ID, SourceLanguage: "en", TargetLanguage: "fr", SourceText: "Hello world"}); err != nil {
		t.Errorf("entry must land in the profile's TM: %v", err)
	}
	if _, err := q.LookupTM(ctx, db.LookupTMParams{TmID: ownTM.ID, SourceLanguage: "en", TargetLanguage: "fr", SourceText: "Hello world"}); err == nil {
		t.Error("entry must NOT land in the project's own TM while linked")
	}
}
