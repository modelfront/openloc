package pipeline_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/settings"
	"openloc/internal/testutil"
)

// A project follows a TM profile with two TMs: the first holds only a fuzzy
// match, the second an exact one. The project's own row stays at the default
// order, so the result shows the order is read from the PROFILE's row.
func translateWithProfileLookupOrder(t *testing.T, lookupOrder string) db.Translation {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenantID, fileID, _ := seedTranslateContext(t, q, []string{"Reset your password"}, 0.75)
	file, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetFileTenant: %v", err)
	}
	project, err := q.GetProjectByID(ctx, file.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}

	tp := testutil.SeedTMProfile(t, q, tenantID, "Ordered profile", 0.75)
	testutil.SetTMLookupOrder(t, q, tp.SettingsProfileID, lookupOrder)

	first := testutil.SeedTM(t, q, tenantID, "First TM")
	second := testutil.SeedTM(t, q, tenantID, "Second TM")
	for i, tm := range []db.TmCollection{first, second} {
		if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
			SettingsProfileID: tp.SettingsProfileID, TmID: tm.ID, Mode: "read_write", Priority: int32(i + 1),
		}); err != nil {
			t.Fatalf("assign TM: %v", err)
		}
	}
	for _, e := range []struct {
		tmID     int64
		src, tgt string
	}{
		{first.ID, "Reset your password!", "Fuzzy from first"},
		{second.ID, "Reset your password", "Exact from second"},
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID: e.tmID, SourceLanguage: "en", TargetLanguage: "fr",
			SourceText: e.src, TargetText: e.tgt, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("InsertTM: %v", err)
		}
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
	return rows[0]
}

func TestIntegrationTMLookupOrder_MatchPercent(t *testing.T) {
	tr := translateWithProfileLookupOrder(t, settings.TMLookupOrderMatchPercent)
	if tr.ActiveText == nil || *tr.ActiveText != "Exact from second" {
		t.Fatalf("match_percent: expected the second TM's exact match, got %v", tr.ActiveText)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent < 100 {
		t.Errorf("expected an exact percent, got %v", tr.TmMatchPercent)
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("an exact match is approved, got status %v", tr.Status)
	}
}

func TestIntegrationTMLookupOrder_TMOrder(t *testing.T) {
	tr := translateWithProfileLookupOrder(t, settings.TMLookupOrderTMOrder)
	if tr.ActiveText == nil || *tr.ActiveText != "Fuzzy from first" {
		t.Fatalf("tm_order: expected the first TM's fuzzy match, got %v", tr.ActiveText)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent >= 100 {
		t.Errorf("expected a fuzzy percent, got %v", tr.TmMatchPercent)
	}
	if tr.Status == db.TranslationStatusApproved {
		t.Error("a fuzzy match must not be approved")
	}
}
