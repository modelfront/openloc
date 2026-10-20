package pipeline_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// md5hex independently mirrors what the pipeline stores/compares for the 101%
// (in-context exact) context hashes. Computed here from scratch (not via the
// production helper) so the assertions don't tautologically agree with a bug.
func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// seedContextProject creates a tenant + read_write TM + en→fr project with no
// MT engine (so the MT phase cleanly skips and only the TM phase runs).
func seedContextProject(t *testing.T, q db.Querier) (tenantID, tmID int64, projectID int64, spID int64) {
	t.Helper()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ContextCorp")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "Context TM")
	project := testutil.SeedProject(t, q, tenant.ID, "Context Project", "en", []string{"fr"})
	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	return tenant.ID, tmColl.ID, project.ID, project.SettingsProfileID
}

// seedDoc creates a file with the given ordered source segments and a fr
// needs_review translation per segment. Returns the file id and the
// per-segment translation ids (index-aligned with sources).
func seedDoc(t *testing.T, q db.Querier, projectID int64, name string, sources []string) (fileID int64, trIDs []int64) {
	t.Helper()
	file := testutil.SeedFile(t, q, projectID, name, db.FileFormatJsonI18n)
	for i, src := range sources {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i+1))
		tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusNeedsReview)
		trIDs = append(trIDs, tr.ID)
	}
	return file.ID, trIDs
}

// trByID fetches one translation row from a file by its translation id.
func trByID(t *testing.T, q db.Querier, fileID, translationID int64) db.Translation {
	t.Helper()
	rows, err := q.ListTranslationsByFile(context.Background(), db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	for _, r := range rows {
		if r.ID == translationID {
			return r
		}
	}
	t.Fatalf("translation %d not found in file %d", translationID, fileID)
	return db.Translation{}
}

// TestIntegrationTMWriteBack_StoresContextHashes proves the write side of
// context matching: confirming a segment stores md5(prev_source) / md5(next_source) of
// its document neighbors on the TM entry, with md5("") at a document boundary.
func TestIntegrationTMWriteBack_StoresContextHashes(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, tmID, projectID, _ := seedContextProject(t, q)
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// Document order: Alpha, Beta, Gamma.
	_, trIDs := seedDoc(t, q, projectID, "doc1.json", []string{"Alpha", "Beta", "Gamma"})

	// Confirm all three so every entry carries neighbor context.
	for idx, target := range map[int]string{0: "AlphaFR", 2: "GammaFR", 1: "BetaFR"} {
		tgt := target
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: trIDs[idx], HumanText: &tgt}); err != nil {
			t.Fatalf("UpdateTranslationHuman idx=%d: %v", idx, err)
		}
		if _, err := svc.SaveToTM(ctx, tenantID, trIDs[idx]); err != nil {
			t.Fatalf("SaveToTM idx=%d: %v", idx, err)
		}
	}

	// Beta (middle): prev=md5("Alpha"), next=md5("Gamma").
	beta, err := q.LookupTM(ctx, db.LookupTMParams{TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr", SourceText: "Beta"})
	if err != nil {
		t.Fatalf("LookupTM Beta: %v", err)
	}
	if beta.PrevSourceHash == nil || *beta.PrevSourceHash != md5hex("Alpha") {
		t.Errorf("Beta prev_source_hash = %v, want %s (md5 Alpha)", beta.PrevSourceHash, md5hex("Alpha"))
	}
	if beta.NextSourceHash == nil || *beta.NextSourceHash != md5hex("Gamma") {
		t.Errorf("Beta next_source_hash = %v, want %s (md5 Gamma)", beta.NextSourceHash, md5hex("Gamma"))
	}

	// Alpha (first): prev=md5("") boundary, next=md5("Beta").
	alpha, err := q.LookupTM(ctx, db.LookupTMParams{TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr", SourceText: "Alpha"})
	if err != nil {
		t.Fatalf("LookupTM Alpha: %v", err)
	}
	if alpha.PrevSourceHash == nil || *alpha.PrevSourceHash != md5hex("") {
		t.Errorf("Alpha prev_source_hash = %v, want %s (md5 empty/boundary)", alpha.PrevSourceHash, md5hex(""))
	}
	if alpha.NextSourceHash == nil || *alpha.NextSourceHash != md5hex("Beta") {
		t.Errorf("Alpha next_source_hash = %v, want %s (md5 Beta)", alpha.NextSourceHash, md5hex("Beta"))
	}
}

// TestIntegrationTranslateFile_ContextMatchScores101 proves the lookup side:
// pretranslating a segment whose source AND both neighbors match a stored TM
// entry scores tm_match_percent=101; the same source with different neighbors
// scores 100. Also covers the document-boundary case (first segment).
func TestIntegrationTranslateFile_ContextMatchScores101(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, _, projectID, _ := seedContextProject(t, q)
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// Source document: Alpha, Beta, Gamma — confirm Alpha (boundary) + Beta
	// (middle) so the TM carries both with their neighbor context.
	srcFile, srcTr := seedDoc(t, q, projectID, "source.json", []string{"Alpha", "Beta", "Gamma"})
	for idx, target := range map[int]string{0: "AlphaFR", 1: "BetaFR"} {
		tgt := target
		if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{ID: srcTr[idx], HumanText: &tgt}); err != nil {
			t.Fatalf("confirm idx=%d: %v", idx, err)
		}
		if _, err := svc.SaveToTM(ctx, tenantID, srcTr[idx]); err != nil {
			t.Fatalf("SaveToTM idx=%d: %v", idx, err)
		}
	}
	_ = srcFile

	// Re-upload: identical document → Alpha + Beta should leverage as 101%.
	reFile, reTr := seedDoc(t, q, projectID, "reupload.json", []string{"Alpha", "Beta", "Gamma"})
	if _, err := svc.TranslateFile(ctx, tenantID, reFile, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile reupload: %v", err)
	}

	beta := trByID(t, q, reFile, reTr[1])
	if beta.TmMatchPercent == nil || *beta.TmMatchPercent != 101 {
		t.Errorf("re-upload Beta tm_match_percent = %v, want 101 (in-context exact)", beta.TmMatchPercent)
	}
	if beta.ActiveText == nil || *beta.ActiveText != "BetaFR" {
		t.Errorf("re-upload Beta active_text = %v, want BetaFR", beta.ActiveText)
	}
	if beta.Status != db.TranslationStatusApproved {
		t.Errorf("re-upload Beta status = %s, want approved (101%% treated like 100%%)", beta.Status)
	}
	// Boundary segment: Alpha is first in both docs (prev = md5("")) → 101.
	alpha := trByID(t, q, reFile, reTr[0])
	if alpha.TmMatchPercent == nil || *alpha.TmMatchPercent != 101 {
		t.Errorf("re-upload Alpha (boundary) tm_match_percent = %v, want 101", alpha.TmMatchPercent)
	}

	// Different document: Beta with DIFFERENT neighbors → plain 100%, not 101.
	diffFile, diffTr := seedDoc(t, q, projectID, "diffctx.json", []string{"Xray", "Beta", "Yankee"})
	if _, err := svc.TranslateFile(ctx, tenantID, diffFile, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile diffctx: %v", err)
	}
	betaDiff := trByID(t, q, diffFile, diffTr[1])
	if betaDiff.TmMatchPercent == nil || *betaDiff.TmMatchPercent != 100 {
		t.Errorf("diff-context Beta tm_match_percent = %v, want 100 (source matches, context differs)", betaDiff.TmMatchPercent)
	}
}
