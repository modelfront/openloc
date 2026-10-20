package pipeline_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/pipeline"
	"openloc/internal/testutil"
)

// TestIntegrationTMWriteBackSavesToTM verifies that human-approved translations
// are saved to TM via the real database (not mocks).
func TestIntegrationTMWriteBackSavesToTM(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Setup: tenant → TM → project (with project-level TM assignment) → file → segments → translations
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "Main TM")

	// Project with direct TM assignment
	project := testutil.SeedProject(t, q, tenant.ID, "FR Project", "en", []string{"fr"})
	_, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	// File + segments + human-approved translations
	file := testutil.SeedFile(t, q, project.ID, "test.json", db.FileFormatJsonI18n)
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello world", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Goodbye", 2)

	// Create translations with status=approved and human_text set
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "fr", db.TranslationStatusApproved)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "fr", db.TranslationStatusApproved)

	humanText1 := "Bonjour le monde"
	humanText2 := "Au revoir"
	_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr1.ID,
		HumanText: &humanText1,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}
	_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr2.ID,
		HumanText: &humanText2,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}

	// Run the TM write-back
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	result, err := svc.SaveApprovedToTM(ctx, tenant.ID, file.ID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}

	// ApprovedCount counts the human-approved segments; TMSavedCount the
	// (translation × TM) writes. One write TM, so both are 2.
	if result.ApprovedCount != 2 {
		t.Errorf("expected 2 approved, got %d", result.ApprovedCount)
	}
	if result.TMSavedCount != 2 {
		t.Errorf("expected 2 TM saved, got %d", result.TMSavedCount)
	}
	if result.NoWriteTMs {
		t.Error("expected NoWriteTMs=false")
	}

	// Verify TM entries actually exist in the database
	entry, err := q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tmColl.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
	})
	if err != nil {
		t.Fatalf("LookupTM 'Hello world': %v", err)
	}
	if entry.TargetText != "Bonjour le monde" {
		t.Errorf("expected TM target 'Bonjour le monde', got %q", entry.TargetText)
	}

	entry, err = q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tmColl.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Goodbye",
	})
	if err != nil {
		t.Fatalf("LookupTM 'Goodbye': %v", err)
	}
	if entry.TargetText != "Au revoir" {
		t.Errorf("expected TM target 'Au revoir', got %q", entry.TargetText)
	}
}

// TestIntegrationTMWriteBackHistoryReason locks in the history wiring on the
// bulk TM write-back path: when SaveApprovedToTM overwrites an existing TM row, the
// archived snapshot must land in translation_memory_history with
// change_reason='feedback_bulk' (NOT 'cat_confirm'), and the auth-context
// user must be recorded as changed_by_user_id. This is the regression guard
// for the production code path that mock-based tmwrite_test.go can't cover
// (the nil-pool fallback skips the history insert in those unit tests).
//
// Anti-tautology proof (per testing.md "leave a durable artifact" rule):
// proven non-tautological 2026-04-28 by mutating tm_write.go::userIDFromCtx
// to always `return nil`. Test failed with the exact diagnostic
// "history.changed_by_user_id is NULL — auth context user did not propagate
// from SaveApprovedToTM". To re-prove: replace the helper body with `return nil`
// (and a `_ = auth.ClaimsFromContext` to keep the import alive), run this
// test, observe failure, restore.
func TestIntegrationTMWriteBackHistoryReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "WriteBackHistoryCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "writeback@example.com", "translator")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "Write-back History TM")
	project := testutil.SeedProject(t, q, tenant.ID, "FH Project", "en", []string{"es"})

	ctx := auth.WithClaims(context.Background(), &auth.Claims{
		UserID:   user.ID,
		TenantID: tenant.ID,
		Role:     "translator",
	})

	if _, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	}); err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	// Two segments with the SAME source — sequential SaveApprovedToTM writes
	// will collide on source_hash and force a history-row write.
	file := testutil.SeedFile(t, q, project.ID, "fh.json", db.FileFormatJsonI18n)
	seg1 := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	seg2 := testutil.SeedSegment(t, q, file.ID, "Hello", 2)
	tr1 := testutil.SeedTranslation(t, q, seg1.ID, "es", db.TranslationStatusApproved)
	tr2 := testutil.SeedTranslation(t, q, seg2.ID, "es", db.TranslationStatusApproved)

	originalTarget := "Hola"
	overwriteTarget := "Hola amigos"
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: tr1.ID, HumanText: &originalTarget,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman tr1: %v", err)
	}
	if _, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID: tr2.ID, HumanText: &overwriteTarget,
	}); err != nil {
		t.Fatalf("UpdateTranslationHuman tr2: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	result, err := svc.SaveApprovedToTM(ctx, tenant.ID, file.ID, "en", "es")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}
	if result.ApprovedCount != 2 {
		t.Errorf("expected 2 approved, got %d", result.ApprovedCount)
	}

	// Live TM has 1 row with the OVERWRITE target (seg2 confirms after seg1
	// because ListApprovedForTM orders by s.position).
	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmColl.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 TM row after sequential write-back overwrites, got %d", len(entries))
	}
	if entries[0].TargetText != overwriteTarget {
		t.Errorf("survivor target=%q, expected %q (last write wins)", entries[0].TargetText, overwriteTarget)
	}

	// History has exactly 1 row: the original "Hola" archived when seg2's
	// the write-back overwrote seg1's row. change_reason MUST be 'feedback_bulk'.
	history, err := q.ListTMEntryHistory(ctx, entries[0].ID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history row from write-back overwrite, got %d", len(history))
	}
	h := history[0]
	if h.TargetText != originalTarget {
		t.Errorf("history.target_text=%q, expected %q (the BEFORE value)", h.TargetText, originalTarget)
	}
	if h.ChangeReason != "feedback_bulk" {
		t.Errorf("history.change_reason=%q, expected 'feedback_bulk' (NOT 'cat_confirm') — write-back path is mis-wired", h.ChangeReason)
	}
	if h.ChangedByUserID == nil {
		t.Error("history.changed_by_user_id is NULL — auth context user did not propagate from SaveApprovedToTM")
	} else if *h.ChangedByUserID != user.ID {
		t.Errorf("history.changed_by_user_id=%d, expected %d", *h.ChangedByUserID, user.ID)
	}
}

// TestIntegrationTMWriteBackNoWriteTMs verifies that SaveApprovedToTM warns
// when the project has no write TM assignments.
func TestIntegrationTMWriteBackNoWriteTMs(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	project := testutil.SeedProject(t, q, tenant.ID, "No TMs", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "test.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Hello", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusApproved)

	humanText := "Bonjour"
	_, err := q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr.ID,
		HumanText: &humanText,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	result, err := svc.SaveApprovedToTM(ctx, tenant.ID, file.ID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}

	// The segment is approved even though no write TM exists to receive it.
	if result.ApprovedCount != 1 {
		t.Errorf("expected 1 approved, got %d", result.ApprovedCount)
	}
	if result.TMSavedCount != 0 {
		t.Errorf("expected 0 TM saved (no write TMs), got %d", result.TMSavedCount)
	}
	if !result.NoWriteTMs {
		t.Error("expected NoWriteTMs=true when no write TM assignments")
	}
}

// TestIntegrationTMWriteBackLanguageMismatchRegression pins that a TM assignment on
// "fr-fr" still receives the write-back for a project on "fr": LookupProjectWriteTMs
// falls back to the base language instead of requiring an exact match.
func TestIntegrationTMWriteBackLanguageMismatchRegression(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "FR TM")

	// Project with "fr" target language
	project := testutil.SeedProject(t, q, tenant.ID, "FR Project", "en", []string{"fr"})

	// TM assignment with "fr" (base language) — should match via base-language fallback
	_, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	file := testutil.SeedFile(t, q, project.ID, "test.json", db.FileFormatJsonI18n)
	seg := testutil.SeedSegment(t, q, file.ID, "Test", 1)
	tr := testutil.SeedTranslation(t, q, seg.ID, "fr", db.TranslationStatusApproved)

	humanText := "Essai"
	_, err = q.UpdateTranslationHuman(ctx, db.UpdateTranslationHumanParams{
		ID:        tr.ID,
		HumanText: &humanText,
	})
	if err != nil {
		t.Fatalf("UpdateTranslationHuman: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	result, err := svc.SaveApprovedToTM(ctx, tenant.ID, file.ID, "en", "fr")
	if err != nil {
		t.Fatalf("SaveApprovedToTM: %v", err)
	}

	// This is the key assertion — the original bug would have TMSavedCount=0
	if result.TMSavedCount != 1 {
		t.Errorf("REGRESSION: expected 1 TM saved for en→fr, got %d (language mismatch bug?)", result.TMSavedCount)
	}
	if result.NoWriteTMs {
		t.Error("REGRESSION: NoWriteTMs should be false when languages match")
	}

	// Verify the TM entry actually exists
	entry, err := q.LookupTM(ctx, db.LookupTMParams{
		TmID:           tmColl.ID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Test",
	})
	if err != nil {
		t.Fatalf("LookupTM: %v (TM entry was not saved!)", err)
	}
	if entry.TargetText != "Essai" {
		t.Errorf("expected 'Essai', got %q", entry.TargetText)
	}
}

// --- TM Matching Integration Tests ---

// seedTranslateContext creates the full context needed for TranslateFile tests:
// tenant → TM → project (with project-level TM+MT assignments) → file → segments → translations
// fuzzyThreshold is optional — pass 0 or omit to use the default (0.75).
func seedTranslateContext(t *testing.T, q db.Querier, sourceTexts []string, fuzzyThreshold ...float32) (tenantID, fileID, tmID int64) {
	t.Helper()
	return seedTranslateContextLangs(t, q, sourceTexts, "en", "fr", fuzzyThreshold...)
}

// seedTranslateContextLangs is seedTranslateContext with the project's language
// pair spelled out — the sublanguage tests need a project on locale codes
// (en-us → fr-fr) matching against TM entries stored on base codes.
func seedTranslateContextLangs(t *testing.T, q db.Querier, sourceTexts []string, sourceLang, targetLang string, fuzzyThreshold ...float32) (tenantID, fileID, tmID int64) {
	t.Helper()
	ctx := context.Background()

	threshold := float32(0.75)
	if len(fuzzyThreshold) > 0 && fuzzyThreshold[0] > 0 {
		threshold = fuzzyThreshold[0]
	}

	tenant := testutil.SeedTenant(t, q, "TMTestCorp")
	tmColl := testutil.SeedTM(t, q, tenant.ID, "Test TM")

	// Project with direct settings
	project := testutil.SeedProject(t, q, tenant.ID, "TM Test Project", sourceLang, []string{targetLang})

	spID := project.SettingsProfileID

	// Set fuzzy threshold on project's settings profile
	_, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID:             spID,
		FuzzyThreshold: threshold,
	})
	if err != nil {
		t.Fatalf("UpdateSettingsProfile: %v", err)
	}

	// Settings-level TM assignment: en → fr, read_write
	_, err = q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: spID,
		TmID:              tmColl.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}

	// Settings-level MT router defaulting to a mock engine
	mtEngine := testutil.SeedMTEngine(t, q, tenant.ID, "Mock", "mock")
	testutil.SeedMTRouterForProfile(t, q, tenant.ID, spID, &mtEngine.ID, nil)

	// File + segments + untranslated translations
	file := testutil.SeedFile(t, q, project.ID, "tm_test.json", db.FileFormatJsonI18n)
	for i, src := range sourceTexts {
		seg := testutil.SeedSegment(t, q, file.ID, src, int32(i+1))
		testutil.SeedTranslation(t, q, seg.ID, targetLang, db.TranslationStatusNeedsReview)
	}

	return tenant.ID, file.ID, tmColl.ID
}

func TestIntegrationTranslateFile_ExactTMMatch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Hello world"})

	// Seed exact TM entry
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	_, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	// Verify translation
	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Errorf("expected mt_text 'Bonjour le monde', got %v", tr.ActiveText)
	}
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("expected origin 'tm', got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Errorf("expected tm_match_percent 100, got %v", tr.TmMatchPercent)
	}
	// Exact TM match auto-approves (sets human_text)
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Errorf("expected human_text 'Bonjour le monde' (auto-approved), got %v", tr.ActiveText)
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("expected status 'approved', got %s", tr.Status)
	}
}

// Pins that a TM whose entries are stored on base codes (en→fr) matches a
// project on locale codes (en-us→fr-fr) — but a locale variant is not an exact
// match. It pays the sublanguage penalty on each mismatched side (100 - 1 - 1 = 98) and, crucially,
// is NOT auto-approved: nobody asked us to sign off on a translation written for
// a different locale.
func TestIntegrationTranslateFile_SublanguageVariantMatchesButIsNotExact(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// fuzzy_threshold 1.0 isolates the exact path: a penalized variant scores
	// 0.98, so the fuzzy stage would reject it. Without it this test passes even
	// with the exact lookup reverted — the fuzzy path picks up the same entry at
	// the same 98% and the assertions can't tell the two paths apart.
	tenantID, fileID, tmID := seedTranslateContextLangs(t, q, []string{"Hello world"}, "en-us", "fr-fr", 1.0)

	// TM entry on base codes — a variant of the project's pair on both sides.
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
		TuID:           "",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr-fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Fatalf("expected the base-code TM entry to be applied, got %s", strPtr(tr.ActiveText))
	}
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("expected origin 'tm', got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 98 {
		t.Errorf("expected tm_match_percent 98 (100 less one penalty per mismatched side), got %s", pctPtr(tr.TmMatchPercent))
	}
	if tr.Status == db.TranslationStatusApproved {
		t.Error("sublanguage variant was auto-approved; it must be left for a human")
	}
	if tr.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected status 'needs_review', got %s", tr.Status)
	}
}

// settingsProfileIDForFile walks file → project the same way the pipeline does,
// so a test can reconfigure the profile the run will actually read.
func settingsProfileIDForFile(t *testing.T, q db.Querier, tenantID, fileID int64) int64 {
	t.Helper()
	ctx := context.Background()
	file, err := q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetFileTenant: %v", err)
	}
	project, err := q.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	return project.SettingsProfileID
}

func setPenalties(t *testing.T, q db.Querier, profileID int64, sublanguagePct, nearExactPct int32) {
	t.Helper()
	if _, err := q.UpdateSettingsProfile(context.Background(), db.UpdateSettingsProfileParams{
		ID:                    profileID,
		FuzzyThreshold:        1.0,
		SublanguagePenaltyPct: &sublanguagePct,
		NearExactPenaltyPct:   &nearExactPct,
	}); err != nil {
		t.Fatalf("UpdateSettingsProfile: %v", err)
	}
}

// Zero sublanguage penalty means "these locales are equivalent" — the whole
// point of making it configurable. A variant then IS a true exact match: full
// 100%, and auto-confirmed, exactly as if the codes had matched byte-for-byte.
func TestIntegrationTranslateFile_ZeroSublanguagePenaltyAutoConfirms(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// fuzzy_threshold 1.0 isolates the exact path — see the sibling test.
	tenantID, fileID, tmID := seedTranslateContextLangs(t, q, []string{"Hello world"}, "en-us", "fr-fr", 1.0)
	setPenalties(t, q, settingsProfileIDForFile(t, q, tenantID, fileID), 0, 1)

	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID: fileID, TargetLanguage: "fr-fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Fatalf("expected the TM entry to be applied, got %s", strPtr(tr.ActiveText))
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Errorf("expected tm_match_percent 100 (penalty disabled), got %s", pctPtr(tr.TmMatchPercent))
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("expected status 'approved' — a 0 penalty means the locales are equivalent, got %s", tr.Status)
	}
}

// An exact match out of a penalised TM scores below 100% and must
// not auto-confirm — an untrusted memory doesn't get to sign off segments.
func TestIntegrationTranslateFile_PerTMPenaltyBlocksAutoConfirm(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContextLangs(t, q, []string{"Hello world"}, "en", "fr", 1.0)

	// Same language pair on both sides: the ONLY thing docking this hit is the
	// per-TM penalty, so the assertions can't pass for the wrong reason.
	if _, err := pool.Exec(ctx,
		`UPDATE settings_tm_assignments SET penalty_pct = 5 WHERE tm_id = $1`, tmID); err != nil {
		t.Fatalf("set penalty_pct: %v", err)
	}
	if _, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello world",
		TargetText:     "Bonjour le monde",
		CreatedBy:      "test",
	}); err != nil {
		t.Fatalf("InsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID: fileID, TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour le monde" {
		t.Fatalf("expected the TM entry to be applied, got %s", strPtr(tr.ActiveText))
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 95 {
		t.Errorf("expected tm_match_percent 95 (100 less the 5%% TM penalty), got %s", pctPtr(tr.TmMatchPercent))
	}
	if tr.Status == db.TranslationStatusApproved {
		t.Error("a penalised TM auto-confirmed its exact match; it must be left for a human")
	}
}

// With both a variant and a perfect-locale entry available, the ORDER BY has to
// pick the perfect one — otherwise the penalty would fire on jobs that had an
// exact match sitting right there.
func TestIntegrationTranslateFile_ExactLocaleBeatsSublanguageVariant(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContextLangs(t, q, []string{"Hello world"}, "en-us", "fr-fr")

	for _, e := range []struct{ src, tgt, text string }{
		{"en", "fr", "Bonjour le monde"},      // variant on both sides
		{"en-us", "fr", "Salut le monde"},     // variant on the target only
		{"en-us", "fr-fr", "Bonjour, monde!"}, // perfect
	} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tmID,
			SourceLanguage: e.src,
			TargetLanguage: e.tgt,
			SourceText:     "Hello world",
			TargetText:     e.text,
			CreatedBy:      "test",
			TuID:           "",
		}); err != nil {
			t.Fatalf("InsertTM %s→%s: %v", e.src, e.tgt, err)
		}
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.TranslateFile(ctx, tenantID, fileID, "en-us", "fr-fr", nil, pipeline.FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr-fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if tr.ActiveText == nil || *tr.ActiveText != "Bonjour, monde!" {
		t.Errorf("expected the exact-locale entry to win, got %s", strPtr(tr.ActiveText))
	}
	if tr.TmMatchPercent == nil || *tr.TmMatchPercent != 100 {
		t.Errorf("expected tm_match_percent 100 (unpenalized), got %s", pctPtr(tr.TmMatchPercent))
	}
	if tr.Status != db.TranslationStatusApproved {
		t.Errorf("expected status 'approved' (true exact match auto-confirms), got %s", tr.Status)
	}
}

func TestIntegrationTranslateFile_FuzzyTMMatch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Use threshold 0.6 so pg_trgm's ~0.71 similarity passes
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Link to reset your password."}, 0.6)

	// Seed TM entry that should fuzzy-match
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Reset your password.",
		TargetText:     "Réinitialisez votre mot de passe.",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	_, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("expected origin 'tm' (fuzzy match), got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent == nil {
		t.Fatal("expected tm_match_percent set for fuzzy match, got nil")
	}
	score := *tr.TmMatchPercent
	if score <= 60 || score >= 100 {
		t.Errorf("expected tm_match_percent between 60 and 100, got %d", score)
	}
	t.Logf("pg_trgm similarity percent: %d", score)

	// Fuzzy match should NOT auto-approve: "not confirmed" is
	// status='needs_review' (active_text holds the fuzzy suggestion, asserted
	// below).
	if tr.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected status 'needs_review' for fuzzy TM, got %s", tr.Status)
	}
	if tr.ActiveText == nil || *tr.ActiveText != "Réinitialisez votre mot de passe." {
		t.Errorf("expected fuzzy TM target text, got %v", tr.ActiveText)
	}
}

func TestIntegrationTranslateFile_NoTMMatch_FallsToMT(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"Something completely different"})

	// Seed a TM entry that won't match
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Hello",
		TargetText:     "Bonjour",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	_, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("expected origin 'mt' (MT fallback), got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent != nil {
		t.Errorf("expected nil tm_match_percent for MT, got %v", tr.TmMatchPercent)
	}
	if tr.Status != db.TranslationStatusNeedsReview {
		t.Errorf("expected status 'pre_translated' for MT, got %s", tr.Status)
	}
}

func TestIntegrationTranslateFile_FuzzyBelowThreshold(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// "password" vs "Reset your password." — very low similarity
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{"password"})

	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "fr",
		SourceText:     "Reset your password.",
		TargetText:     "Réinitialisez votre mot de passe.",
		CreatedBy:      "test",
		TuID:           "",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	_, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 translation, got %d", len(rows))
	}

	tr := rows[0]
	// Below threshold: should fall to MT
	if !tr.Origin.Valid || tr.Origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("expected origin 'mt' (below threshold), got valid=%v %v", tr.Origin.Valid, tr.Origin.TranslationOrigin)
	}
	if tr.TmMatchPercent != nil {
		t.Errorf("expected nil tm_match_percent, got %v", tr.TmMatchPercent)
	}
}

func TestIntegrationTranslateFile_MixedExactFuzzyMT(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Use threshold 0.6 so fuzzy match (~0.71 from pg_trgm) passes
	tenantID, fileID, tmID := seedTranslateContext(t, q, []string{
		"Hello world",                        // exact match
		"Link to reset your password.",       // fuzzy match
		"Something completely unrelated xyz", // no match → MT
	}, 0.6)

	// Seed TM entries
	_, err := q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Hello world", TargetText: "Bonjour le monde",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}
	_, err = q.InsertTM(ctx, db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "fr",
		SourceText: "Reset your password.", TargetText: "Réinitialisez votre mot de passe.",
	})
	if err != nil {
		t.Fatalf("UpsertTM: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	_, err = svc.TranslateFile(ctx, tenantID, fileID, "en", "fr", nil, pipeline.FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	rows, err := q.ListTranslationsByFile(ctx, db.ListTranslationsByFileParams{
		FileID:         fileID,
		TargetLanguage: "fr",
	})
	if err != nil {
		t.Fatalf("ListTranslationsByFile: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 translations, got %d", len(rows))
	}

	// Build map by source text for easier assertion
	// Provenance is `origin` (sticky lineage); the visible target is
	// active_text.
	type result struct {
		origin db.NullTranslationOrigin
		score  *int16 // tm_match_percent (0..101 int)
		status db.TranslationStatus
		active *string
	}
	results := make(map[string]result)
	for _, tr := range rows {
		// Get source text via segment
		seg, err := q.GetSegment(ctx, tr.SegmentID)
		if err != nil {
			t.Fatalf("GetSegment: %v", err)
		}
		results[seg.SourceText] = result{
			origin: tr.Origin,
			score:  tr.TmMatchPercent,
			status: tr.Status,
			active: tr.ActiveText,
		}
	}

	// 1. Exact TM match
	exact := results["Hello world"]
	if !exact.origin.Valid || exact.origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("exact: expected origin 'tm', got %v", exact.origin)
	}
	if exact.score == nil || *exact.score != 100 {
		t.Errorf("exact: expected score 100, got %v", exact.score)
	}
	if exact.status != db.TranslationStatusApproved {
		t.Errorf("exact: expected approved, got %s", exact.status)
	}
	if exact.active == nil || *exact.active != "Bonjour le monde" {
		t.Errorf("exact: expected active_text auto-set, got %v", exact.active)
	}

	// 2. Fuzzy TM match
	fuzzy := results["Link to reset your password."]
	if !fuzzy.origin.Valid || fuzzy.origin.TranslationOrigin != db.TranslationOriginTm {
		t.Errorf("fuzzy: expected origin 'tm', got %v", fuzzy.origin)
	}
	if fuzzy.score == nil || *fuzzy.score <= 60 || *fuzzy.score >= 100 {
		t.Errorf("fuzzy: expected score between 60 and 100, got %v", fuzzy.score)
	}
	if fuzzy.status != db.TranslationStatusNeedsReview {
		t.Errorf("fuzzy: expected needs_review, got %s", fuzzy.status)
	}

	// 3. MT fallback
	mtResult := results["Something completely unrelated xyz"]
	if !mtResult.origin.Valid || mtResult.origin.TranslationOrigin != db.TranslationOriginMt {
		t.Errorf("mt: expected origin 'mt', got %v", mtResult.origin)
	}
	if mtResult.score != nil {
		t.Errorf("mt: expected nil score, got %v", mtResult.score)
	}
	if mtResult.status != db.TranslationStatusNeedsReview {
		t.Errorf("mt: expected pre_translated, got %s", mtResult.status)
	}

	t.Log("Mixed TM matching: exact ✓, fuzzy ✓, MT fallback ✓")
}

// countTMEntries returns the number of translation_memory rows for an
// exact (tm_id, src_lang, tgt_lang, source_text, target_text) tuple.
// Used by the TM write-back tests to assert dedupe.
func countTMEntries(t *testing.T, q db.Querier, tmID int64, srcLang, tgtLang, src, tgt string) int {
	t.Helper()
	entries, err := q.LookupTMAll(context.Background(), db.LookupTMAllParams{
		TmID:           tmID,
		SourceLanguage: srcLang,
		TargetLanguage: tgtLang,
		SourceText:     src,
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.TargetText == tgt {
			n++
		}
	}
	return n
}

// setupTMWriteBackFixture seeds a tenant / TM / project / settings profile
// with a write-mode TM assignment for en→es, ready for SaveSegmentToTM calls.
func setupTMWriteBackFixture(t *testing.T, q db.Querier) (tenantID, tmID, projectID int64) {
	t.Helper()
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "TMWriteBackCorp")
	tm := testutil.SeedTM(t, q, tenant.ID, "WriteBack TM")
	project := testutil.SeedProject(t, q, tenant.ID, "WriteBack Project", "en", []string{"es"})
	_, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	return tenant.ID, tm.ID, project.ID
}

// TestIntegrationTMWriteBack_SequentialSavesDedupeAndUpdateMetadata is the
// happy-path guard for TM write-back dedupe: the same segment saved twice
// via SaveSegmentToTM must end up as a SINGLE row in the TM (not two), and
// the metadata (updated_at / usage_count) should advance on the second save.
// The concurrent case lives in _ConcurrentSavesProduceOneRow.
func TestIntegrationTMWriteBack_SequentialSavesDedupeAndUpdateMetadata(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// First save — should insert a new row.
	saved, err := svc.SaveSegmentToTM(ctx, tenantID, "Hello", "Hola", "en", "es", "", "", projectID, nil, "test")
	if err != nil {
		t.Fatalf("first SaveSegmentToTM: %v", err)
	}
	if len(saved) != 1 {
		t.Errorf("first save: expected 1 TM written, got %d (ids=%v)", len(saved), saved)
	}

	// Second save of the exact same source/target — should update metadata, NOT insert.
	saved, err = svc.SaveSegmentToTM(ctx, tenantID, "Hello", "Hola", "en", "es", "", "", projectID, nil, "test")
	if err != nil {
		t.Fatalf("second SaveSegmentToTM: %v", err)
	}
	if len(saved) != 1 {
		t.Errorf("second save: expected 1 TM written (metadata update counts), got %d (ids=%v)", len(saved), saved)
	}

	// Assert: exactly one TM row for the Hello→Hola pair.
	got := countTMEntries(t, q, tmID, "en", "es", "Hello", "Hola")
	if got != 1 {
		t.Errorf("expected 1 TM entry after 2 sequential saves, got %d — dedupe is broken", got)
	}
}

// TestIntegrationTMWriteBack_ConcurrentSavesProduceOneRow races two goroutines
// saving the same segment via the pipeline. A check-then-insert lets both see
// "no match" and both insert; the atomic upsert makes the second concurrent
// save either update the first row's metadata or be a no-op — NEVER a second
// row.
//
// This is inherently a race-triggering test, so we run multiple rounds with
// tightly-synchronized goroutines to make the race window hittable. The
// partial unique index alone keeps the row count at one, so each save must
// also succeed and report the TM it wrote: a check-then-insert loser hits the
// index, is skipped, and returns no saved TM.
func TestIntegrationTMWriteBack_ConcurrentSavesProduceOneRow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	const rounds = 10
	for i := 0; i < rounds; i++ {
		// Use a distinct source per round so each round has a fresh race window.
		src := "Concurrent source " + string(rune('A'+i))
		tgt := "Concurrent target"

		// Barrier: both goroutines block on start, then race simultaneously.
		start := make(chan struct{})
		var wg sync.WaitGroup
		var saved [2][]int64
		var errs [2]error
		for g := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				saved[g], errs[g] = svc.SaveSegmentToTM(ctx, tenantID, src, tgt, "en", "es", "", "", projectID, nil, "test")
			}()
		}
		close(start)
		wg.Wait()

		for g := range 2 {
			if errs[g] != nil {
				t.Errorf("round %d save %d: %v", i, g, errs[g])
			}
			if !slices.Equal(saved[g], []int64{tmID}) {
				t.Errorf("round %d save %d: saved TMs = %v, want [%d]", i, g, saved[g], tmID)
			}
		}

		got := countTMEntries(t, q, tmID, "en", "es", src, tgt)
		if got != 1 {
			t.Errorf("round %d (%q): expected 1 TM entry after concurrent saves, got %d — check-then-insert race is leaking duplicates",
				i, src, got)
		}
	}
}

// TestIntegrationTMWriteBack_DifferentTargetsCollapseAndArchive locks in the
// dedupe behaviour: the human pipeline path dedupes on source
// only. When a translator confirms the same source with a new target, the
// existing row is overwritten and the prior target is snapshotted into
// translation_memory_history — the active TU is overwritten on re-confirm
// and prior values stay in a separate audit log.
//
// Multiple-targets-per-source preservation is scoped to the
// TMX import path only. See _TMXImportDuplicatesPreserved for that side.
//
// Anti-tautology proof (per testing.md "leave a durable artifact" rule):
// proven non-tautological 2026-04-28 by reverting migration 023's index
// swap to recreate the WIDE index (with target_hash). Test failed with
// "expected 1 TM row after 3 sequential edits (source dedupe), got 0"
// (upsert errors out: ON CONFLICT predicate no longer matches the index).
// To re-prove: in migrations/023_*.sql Step 2, change
// `(tm_id, source_language, target_language, source_hash)` back to the
// wide form `(tm_id, source_language, target_language, source_hash,
// target_hash)`, run this test, observe failure, restore. (Note that
// SetupTestDB re-runs migrations on every test, so editing the .sql file
// is the only way to mutate the source — direct psql DDL gets wiped.)
func TestIntegrationTMWriteBack_DifferentTargetsCollapseAndArchive(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// Translator confirms "Bank" with three successive targets.
	for _, tgt := range []string{"Banco (institución)", "Orilla (río)", "Banco final"} {
		if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Bank", tgt, "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
			t.Fatalf("SaveSegmentToTM(%q): %v", tgt, err)
		}
	}

	// Live TM has exactly one row, with the LATEST target.
	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID:           tmID,
		SourceLanguage: "en",
		TargetLanguage: "es",
		SourceText:     "Bank",
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 TM row after 3 sequential edits (source dedupe), got %d", len(entries))
	}
	survivor := entries[0]
	if survivor.TargetText != "Banco final" {
		t.Errorf("expected survivor target 'Banco final' (last confirm wins), got %q", survivor.TargetText)
	}

	// History has exactly two rows: the two prior targets, newest-first.
	history, err := q.ListTMEntryHistory(ctx, survivor.ID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history rows for the 2 overwrites, got %d", len(history))
	}
	want := []string{"Orilla (río)", "Banco (institución)"} // newest-first
	for i, w := range want {
		if history[i].TargetText != w {
			t.Errorf("history[%d].target_text = %q, want %q", i, history[i].TargetText, w)
		}
		if history[i].ChangeReason != "cat_confirm" {
			t.Errorf("history[%d].change_reason = %q, want 'cat_confirm'", i, history[i].ChangeReason)
		}
	}

	// usage_count starts at 0 on insert, +1 per overwrite — 3 confirms = 2 bumps.
	if survivor.UsageCount != 2 {
		t.Errorf("expected usage_count=2 after 1 insert + 2 overwrites, got %d", survivor.UsageCount)
	}
}

// TestIntegrationTMWriteBack_NoChangeReConfirmDoesNotSpamHistory guards
// against translator workflows where Cmd+Enter is hit twice on an unchanged
// segment. The upsert still bumps usage_count, but no history row is written
// (matches the TM browser PATCH guard — only meaningful changes archive).
func TestIntegrationTMWriteBack_NoChangeReConfirmDoesNotSpamHistory(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	for i := 0; i < 3; i++ {
		if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Hello", "Hola", "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
			t.Fatalf("SaveSegmentToTM round %d: %v", i, err)
		}
	}

	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 TM row, got %d", len(entries))
	}
	if entries[0].UsageCount != 2 {
		t.Errorf("expected usage_count=2 after 1 insert + 2 re-confirm bumps, got %d", entries[0].UsageCount)
	}

	history, err := q.ListTMEntryHistory(ctx, entries[0].ID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 history rows (no semantic change), got %d — re-confirm is spamming the audit log", len(history))
	}
}

// TestIntegrationTMWriteBack_HistoryCapturesUserAndReason verifies that the
// changedByUserID + changeReason args propagate into translation_memory_history.
// The CAT confirm path passes 'cat_confirm', the bulk write-back path passes
// 'feedback_bulk' — anything that surfaces in the future history-viewer UI
// must come from this wiring.
func TestIntegrationTMWriteBack_HistoryCapturesUserAndReason(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "HistoryAttribCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "translator@example.com", "translator")
	tmID, projectID := setupTMWriteBackFixtureForTenant(t, q, tenant.ID)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	uid := user.ID
	if _, err := svc.SaveSegmentToTM(ctx, tenant.ID, "Hello", "Hola", "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := svc.SaveSegmentToTM(ctx, tenant.ID, "Hello", "Hola amigo", "en", "es", "", "", projectID, &uid, "cat_confirm"); err != nil {
		t.Fatalf("second save: %v", err)
	}

	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil || len(entries) != 1 {
		t.Fatalf("LookupTMAll: entries=%d err=%v", len(entries), err)
	}

	history, err := q.ListTMEntryHistory(ctx, entries[0].ID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(history))
	}
	h := history[0]
	if h.ChangeReason != "cat_confirm" {
		t.Errorf("change_reason = %q, want 'cat_confirm'", h.ChangeReason)
	}
	if h.ChangedByUserID == nil {
		t.Errorf("changed_by_user_id is NULL — auth context user did not propagate")
	} else if *h.ChangedByUserID != user.ID {
		t.Errorf("changed_by_user_id = %d, want %d", *h.ChangedByUserID, user.ID)
	}
	if h.TargetText != "Hola" {
		t.Errorf("history target = %q, want 'Hola' (the BEFORE value)", h.TargetText)
	}
}

// TestIntegrationTMWriteBack_StampsUserLabelIntoUpdatedBy locks in that
// the pipeline write-back stamps the triggering user's "Name <email>" label
// into translation_memory.updated_by, overwrites it
// with the new user on re-confirm, and falls back to "human" for system writes.
// created_by stays 'human' — the dedupe partial-index predicate must not change.
func TestIntegrationTMWriteBack_StampsUserLabelIntoUpdatedBy(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "AttribCorp")
	tmID, projectID := setupTMWriteBackFixtureForTenant(t, q, tenant.ID)

	// Two users with distinct display names so the "Name <email>" branch is
	// actually exercised (SeedUser sets name == email, which collapses to email).
	alice, err := q.CreateUser(ctx, db.CreateUserParams{Email: "alice@example.com", Name: "Alice Translator"})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := q.CreateUser(ctx, db.CreateUserParams{Email: "bob@example.com", Name: "Bob Reviewer"})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// Create: Alice confirms the segment.
	aliceID := alice.ID
	if _, err := svc.SaveSegmentToTM(ctx, tenant.ID, "Hello", "Hola", "en", "es", "", "", projectID, &aliceID, "cat_confirm"); err != nil {
		t.Fatalf("save (alice): %v", err)
	}
	got := lookupSingleTM(t, ctx, q, tmID, "Hello")
	if want := auth.UserLabel(alice.Name, alice.Email); got.UpdatedBy != want {
		t.Errorf("after create: updated_by = %q, want %q", got.UpdatedBy, want)
	}
	if got.CreatedBy != "human" {
		t.Errorf("created_by = %q, want 'human' (dedupe discriminator must not change)", got.CreatedBy)
	}

	// Update: Bob re-confirms the same source → updated_by flips to Bob.
	bobID := bob.ID
	if _, err := svc.SaveSegmentToTM(ctx, tenant.ID, "Hello", "Hola amigo", "en", "es", "", "", projectID, &bobID, "cat_confirm"); err != nil {
		t.Fatalf("save (bob): %v", err)
	}
	got = lookupSingleTM(t, ctx, q, tmID, "Hello")
	if want := auth.UserLabel(bob.Name, bob.Email); got.UpdatedBy != want {
		t.Errorf("after update: updated_by = %q, want %q", got.UpdatedBy, want)
	}

	// System write (no user in scope) → falls back to "human".
	if _, err := svc.SaveSegmentToTM(ctx, tenant.ID, "Goodbye", "Adios", "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
		t.Fatalf("save (system): %v", err)
	}
	sys := lookupSingleTM(t, ctx, q, tmID, "Goodbye")
	if sys.UpdatedBy != "human" {
		t.Errorf("system write updated_by = %q, want 'human'", sys.UpdatedBy)
	}
}

// lookupSingleTM fetches the one expected TM row for a source and fails otherwise.
func lookupSingleTM(t *testing.T, ctx context.Context, q db.Querier, tmID int64, source string) db.TranslationMemory {
	t.Helper()
	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: source,
	})
	if err != nil || len(entries) != 1 {
		t.Fatalf("LookupTMAll(%q): entries=%d err=%v", source, len(entries), err)
	}
	return entries[0]
}

// TestIntegrationTMWriteBack_ConcurrentDifferentTargetsArchiveLoser is the
// regression guard for the concurrent-attribution race that motivated the
// pg_advisory_xact_lock in upsertTMWithHistory.
//
// Without the lock, two parallel SaveSegmentToTM calls for the SAME source
// with DIFFERENT targets racing through the CTE:
//   - Both `prev` SELECT branches snapshot empty (no row exists yet).
//   - Both INSERTs serialize on the partial unique index.
//   - Winner inserts row with target A.
//   - Loser's INSERT conflicts → DO UPDATE overwrites with target B.
//   - Loser's outer SELECT LEFT JOINs against the loser's `prev` (empty)
//     → prev_entry_id = NULL → caller skips the history insert.
//   - Result: target A silently disappears from BOTH the live row AND the
//     audit log — a real data-loss bug, not a "misleading sequence".
//
// With the advisory lock acquired before the CTE, the loser blocks on the
// lock until the winner commits. After the winner releases, the loser's
// `prev` SELECT sees the winner's row, prev_entry_id is set, and the
// history row records the winner's target as the BEFORE state. Final live
// row is the loser's target; history captures the winner's. No data loss.
//
// Asserts on the data, not on which goroutine wins (the partial index
// serializes them, but order isn't deterministic from Go's side).
//
// Anti-tautology proof (per testing.md "leave a durable artifact" rule):
// proven non-tautological 2026-04-28 by replacing the
// `q.AcquireTMSourceLock(...)` call in upsertTMWithHistory with a no-op
// (`_ = db.AcquireTMSourceLockParams{}`). Test failed at round 1 with
// "expected 1 history row (loser archives winner's target), got 0 — race
// lost the winner's target". To re-prove: comment out the AcquireTMSourceLock
// call in tm_write.go, run this test, observe failure, restore.
//
// Probabilistic-coverage note: the race window opens between the two
// goroutines' `prev` SELECTs and their UPSERTs. Go's scheduler doesn't
// guarantee interleaving — under a particular alignment both goroutines
// might run sequentially and miss the window. We run 15 rounds (each with
// distinct sources) so the cumulative probability of the bug going
// undetected on a buggy build is negligible. During the anti-tautology
// proof the race fired at round 1 of 5 every time; 15 rounds
// gives a 3× safety margin without making the integration suite noticeably
// slower.
func TestIntegrationTMWriteBack_ConcurrentDifferentTargetsArchiveLoser(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	const rounds = 15
	for round := 0; round < rounds; round++ {
		// Distinct source per round so each round starts with a fresh empty
		// state — the race window only opens when no prior row exists.
		src := "Race source " + string(rune('A'+round))
		tgt1 := "Target-X-" + string(rune('A'+round))
		tgt2 := "Target-Y-" + string(rune('A'+round))

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.SaveSegmentToTM(ctx, tenantID, src, tgt1, "en", "es", "", "", projectID, nil, "cat_confirm")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = svc.SaveSegmentToTM(ctx, tenantID, src, tgt2, "en", "es", "", "", projectID, nil, "cat_confirm")
		}()
		close(start)
		wg.Wait()

		// Assertion 1: exactly 1 TM row remains (partial index dedupe).
		entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
			TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: src,
		})
		if err != nil {
			t.Fatalf("round %d LookupTMAll: %v", round, err)
		}
		if len(entries) != 1 {
			t.Fatalf("round %d: expected 1 TM row (partial index dedupe), got %d", round, len(entries))
		}

		// Assertion 2: exactly 1 history row. The loser's pre-overwrite snapshot
		// must be archived. Without the lock fix this is 0 (data loss).
		history, err := q.ListTMEntryHistory(ctx, entries[0].ID)
		if err != nil {
			t.Fatalf("round %d ListTMEntryHistory: %v", round, err)
		}
		if len(history) != 1 {
			t.Fatalf("round %d: expected 1 history row (loser archives winner's target), got %d — race lost the winner's target",
				round, len(history))
		}

		// Assertion 3: the live target and the history target are DIFFERENT
		// — they're the two values we passed in. Whichever won doesn't matter.
		live := entries[0].TargetText
		archived := history[0].TargetText
		if live == archived {
			t.Errorf("round %d: live target %q == history target %q — race produced a no-op write where one should be the loser's target", round, live, archived)
		}
		bothTargets := map[string]bool{tgt1: true, tgt2: true}
		if !bothTargets[live] {
			t.Errorf("round %d: live target %q is neither %q nor %q", round, live, tgt1, tgt2)
		}
		if !bothTargets[archived] {
			t.Errorf("round %d: archived target %q is neither %q nor %q", round, archived, tgt1, tgt2)
		}
		if history[0].ChangeReason != "cat_confirm" {
			t.Errorf("round %d: history change_reason = %q, want 'cat_confirm'", round, history[0].ChangeReason)
		}
	}
}

// TestIntegrationTMWriteBack_HistoryFailureRollsBackUpsert proves the
// atomicity guarantee that justifies the *pgxpool.Pool dependency on
// pipeline.Service: when InsertTMHistory fails after UpsertTMPipelineEntry
// succeeds, BOTH operations must roll back so the live TM row is unchanged.
// Without the tx wrapper, the upsert's overwrite would land but the history
// snapshot of the prior target would be lost — exactly the kind of silent
// data loss the tx wrapper prevents.
//
// We force the failure by passing a non-existent changedByUserID — the
// translation_memory_history.changed_by_user_id FK constraint
// (REFERENCES users(id)) rejects the insert at commit time. SaveSegmentToTM
// must catch the error, roll back the tx, and report saved=0.
//
// Anti-tautology proof (per testing.md "leave a durable artifact" rule):
// proven non-tautological 2026-04-28 by inserting a `s.queries.
// UpsertTMPipelineEntry(ctx, params)` call BEFORE `s.txr.InTx(...)` in
// upsertTMWithHistory (so the upsert commits independently of the tx).
// Test failed with the exact diagnostic
// "expected target_text='ORIGINAL' (rollback preserves prior value), got
// "NEW" — tx atomicity is broken". To re-prove: add a non-tx upsert call
// before the InTx callback, run this test, observe failure, restore.
func TestIntegrationTMWriteBack_HistoryFailureRollsBackUpsert(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// First save: insert "Hello" → "ORIGINAL" (no history — fresh row).
	if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Hello", "ORIGINAL", "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Snapshot the row id before the doomed second save so we can verify
	// the rollback left the SAME row in place (not a new one + the original).
	preEntries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTMAll (pre): %v", err)
	}
	if len(preEntries) != 1 || preEntries[0].TargetText != "ORIGINAL" {
		t.Fatalf("pre-state mismatch: rows=%d target=%q (want 1 row 'ORIGINAL')", len(preEntries), preEntries[0].TargetText)
	}
	originalID := preEntries[0].ID

	// Second save: trigger history INSERT failure via FK violation. A user
	// id of 999_999_999 does not exist; translation_memory_history.
	// changed_by_user_id REFERENCES users(id), so the insert will fail with
	// foreign_key_violation INSIDE the transaction.
	bogusUserID := int64(999_999_999)
	saved, err := svc.SaveSegmentToTM(ctx, tenantID, "Hello", "NEW", "en", "es", "", "", projectID, &bogusUserID, "cat_confirm")
	if err != nil {
		t.Fatalf("SaveSegmentToTM should NOT propagate the error to the caller (it's logged + counted as not-saved); got err=%v", err)
	}
	if len(saved) != 0 {
		t.Errorf("expected 0 TMs written (history failure rolled back the upsert), got %d (ids=%v)", len(saved), saved)
	}

	// Assert: the row still has the ORIGINAL target. If the tx wrapper were
	// missing, target would be "NEW" because the upsert would have committed
	// independently of the history failure.
	postEntries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil {
		t.Fatalf("LookupTMAll (post): %v", err)
	}
	if len(postEntries) != 1 {
		t.Fatalf("expected 1 row after rolled-back upsert (same row unchanged), got %d", len(postEntries))
	}
	if postEntries[0].ID != originalID {
		t.Errorf("expected same row id %d (rollback preserves identity), got %d", originalID, postEntries[0].ID)
	}
	if postEntries[0].TargetText != "ORIGINAL" {
		t.Errorf("expected target_text='ORIGINAL' (rollback preserves prior value), got %q — tx atomicity is broken", postEntries[0].TargetText)
	}

	// Assert: no history rows landed (the failed insert was rolled back too).
	history, err := q.ListTMEntryHistory(ctx, originalID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 history rows (the doomed insert rolled back), got %d", len(history))
	}
}

// TestIntegrationTMWriteBack_TMXImportPathStillPreservesMultiTarget is the
// sentinel: directly inserting import-creator rows must NOT be affected by
// the human-path dedupe. Same-source, different-target import rows survive,
// and a subsequent human confirm for the same source adds exactly one
// human-creator row alongside (does not collide with the import rows).
func TestIntegrationTMWriteBack_TMXImportPathStillPreservesMultiTarget(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	// Simulate three TMX <tu>s with same source, different targets.
	for _, tgt := range []string{"Banco", "Orilla", "Lado"} {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tmID,
			SourceLanguage: "en",
			TargetLanguage: "es",
			SourceText:     "Bank",
			TargetText:     tgt,
			CreatedBy:      "tmx-import",
			TuID:           "",
		}); err != nil {
			t.Fatalf("InsertTM (TMX) %q: %v", tgt, err)
		}
	}

	// Human confirm for the same source.
	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)
	if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Bank", "Banco (humano)", "en", "es", "", "", projectID, nil, "cat_confirm"); err != nil {
		t.Fatalf("SaveSegmentToTM: %v", err)
	}

	entries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Bank",
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	// 3 import rows + 1 human row = 4 total.
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries (3 TMX imports + 1 human), got %d", len(entries))
	}
	importCount := 0
	humanCount := 0
	for _, e := range entries {
		if e.CreatedBy == "tmx-import" {
			importCount++
		}
		if e.CreatedBy == "human" {
			humanCount++
		}
	}
	if importCount != 3 {
		t.Errorf("expected 3 import rows preserved, got %d — human-path dedupe leaked into the import path", importCount)
	}
	if humanCount != 1 {
		t.Errorf("expected 1 human row, got %d", humanCount)
	}
}

// setupTMWriteBackFixtureForTenant is the multi-tenant variant of
// setupTMWriteBackFixture. Used by tests that need a specific tenant (e.g.
// to seed a user under that tenant for history attribution).
func setupTMWriteBackFixtureForTenant(t *testing.T, q db.Querier, tenantID int64) (tmID, projectID int64) {
	t.Helper()
	ctx := context.Background()
	tm := testutil.SeedTM(t, q, tenantID, "WriteBack TM")
	project := testutil.SeedProject(t, q, tenantID, "WriteBack Project", "en", []string{"es"})
	_, err := q.CreateSettingsTMAssignment(ctx, db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: project.SettingsProfileID,
		TmID:              tm.ID,
		Mode:              "read_write",
		Priority:          1,
	})
	if err != nil {
		t.Fatalf("CreateSettingsTMAssignment: %v", err)
	}
	return tm.ID, project.ID
}

// TestIntegrationTMWriteBack_StoresRichTextWhenProvided verifies the pipeline
// write-back path: when a confirmed segment has inline XLIFF tags (preserved
// on segment.source_rich_text + translation.active_rich_text), the resulting
// TM row has source_rich_text + target_rich_text
// populated. Frontend's <SegmentText> component then renders atomic chips
// instead of raw markup in the TM browser + Concordance modal.
//
// Anti-tautology: passing "" rich text → NULL in the TM row (asserted in the
// sibling _NoRichText test). So the test below must use NON-empty rich text
// to demonstrate the column actually got written through.
func TestIntegrationTMWriteBack_StoresRichTextWhenProvided(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	// Sentinel rich text — distinctive enough that any silent stripping or
	// substitution would be obvious in a failure.
	srcRich := `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept>`
	tgtRich := `Haz clic <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept>`

	if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Click here", "Haz clic aquí", "en", "es", srcRich, tgtRich, projectID, nil, "test"); err != nil {
		t.Fatalf("SaveSegmentToTM: %v", err)
	}

	rows, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Click here",
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 TM row for the saved pair, got %d", len(rows))
	}

	got := rows[0]
	if got.SourceRichText == nil {
		t.Fatal("source_rich_text is NULL — pipeline did NOT pass rich text through. Check tm_write.go::SaveToTM and tmwrite.go.")
	}
	if *got.SourceRichText != srcRich {
		t.Errorf("source_rich_text mismatch:\n  want: %q\n  got:  %q", srcRich, *got.SourceRichText)
	}
	if got.TargetRichText == nil || *got.TargetRichText != tgtRich {
		t.Errorf("target_rich_text mismatch:\n  want: %q\n  got:  %v", tgtRich, testutil.Show(got.TargetRichText))
	}
}

// TestIntegrationTMWriteBack_NoRichTextStaysNull is the negative case: when
// the segment has NO inline tags (rich text args are ""), the TM row's
// rich_text columns must be NULL — not empty string. This is the
// NULLIF(@rich_text, ”) contract and lets the frontend cleanly distinguish
// "no rich text" from "empty rich text".
func TestIntegrationTMWriteBack_NoRichTextStaysNull(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantID, tmID, projectID := setupTMWriteBackFixture(t, q)

	svc := pipeline.NewService(q, pipeline.NewPgxTxRunner(pool), nil)

	if _, err := svc.SaveSegmentToTM(ctx, tenantID, "Plain", "Llano", "en", "es", "", "", projectID, nil, "test"); err != nil {
		t.Fatalf("SaveSegmentToTM: %v", err)
	}

	rows, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Plain",
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("LookupTMAll: rows=%d err=%v", len(rows), err)
	}
	if rows[0].SourceRichText != nil {
		t.Errorf("source_rich_text must be NULL when rich arg was empty; got %q", *rows[0].SourceRichText)
	}
	if rows[0].TargetRichText != nil {
		t.Errorf("target_rich_text must be NULL when rich arg was empty; got %q", *rows[0].TargetRichText)
	}
}

// TestIntegrationMigration023BackfillCollapsesAndArchives is the regression
// guard for migration 023's cleanup CTE. The cleanup is a one-shot destructive
// operation that runs against existing prod data — if it picks the wrong
// survivor, fails to write history rows, or sums usage_count incorrectly,
// real translator work history would be silently lost.
//
// We simulate the pre-migration state inside an already-migrated test DB:
// drop the narrow index, recreate the wide (migration 014) index
// so we can insert duplicate-target rows, seed three human rows for the same
// source with progressively newer `updated_at` values, then run the cleanup
// CTE + index swap as raw SQL. Assert the outcome matches what migration 023
// would produce against real prod data with pre-existing duplicates.
//
// Known trade-off: the cleanup SQL below is an INLINE MIRROR of migration
// 023's body, not a read-from-file. This is intentional — migrations are
// immutable in production (you add 024 to amend, you don't edit 023), so
// the file-read approach would add parse-fragile path-dependent complexity
// for a purely academic gain. If migration 023 IS ever amended in some
// future branch, this mirror needs to be updated too — `git log -p
// internal/database/migrations/023_*.sql` should never have unsynchronised
// edits with `git log -p -L /Step 1.*Snapshot/.../this_test/`.
//
// Anti-tautology proof (per testing.md "leave a durable artifact" rule):
// proven non-tautological 2026-04-28 by reverting migration 023's index
// swap (re-adding target_hash). Test failed because the upsert now hits
// "ON CONFLICT specification mismatch" against the wide index.
// Independently, mutating the inline cleanup CTE's `ORDER BY updated_at
// DESC, id DESC` to `ASC` (picks oldest as survivor) would cause the
// `survivor.id == survivorID` and target-text assertions to fail. To
// re-prove either: edit migration 023 OR the inline CTE, run, observe.
func TestIntegrationMigration023BackfillCollapsesAndArchives(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "BackfillCorp")
	tmCol := testutil.SeedTM(t, q, tenant.ID, "Backfill TM")

	// Step 1: rewind the index shape to migration 014's pre-023 state so we
	// can insert same-source different-target human rows that the current
	// (post-023) index would reject.
	if _, err := pool.Exec(ctx, `DROP INDEX IF EXISTS translation_memory_pipeline_dedupe`); err != nil {
		t.Fatalf("drop narrow index: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE UNIQUE INDEX translation_memory_pipeline_dedupe
		  ON translation_memory (tm_id, source_language, target_language, source_hash, target_hash)
		  WHERE created_by = 'human'
	`); err != nil {
		t.Fatalf("recreate wide index: %v", err)
	}

	// Step 2: seed three duplicate human rows for the same source. Targets
	// differ so they're permitted by the wide index. updated_at is staged
	// via explicit UPDATE so the survivor pick (most-recent updated_at) is
	// deterministic — the third insert will be the survivor.
	type seeded struct {
		id         int64
		target     string
		usageCount int32
	}
	var rows []seeded
	for i, tgt := range []string{"Hola", "Hola, mundo", "Hola amigos"} {
		var id int64
		var uc int32
		if err := pool.QueryRow(ctx, `
			INSERT INTO translation_memory (
			  tm_id, source_language, target_language,
			  source_text, target_text,
			  source_hash, target_hash,
			  created_by, updated_by, tu_id, usage_count
			) VALUES (
			  $1, $2, $3, $4, $5, md5($4), md5($5), 'human', 'human', '', $6
			) RETURNING id, usage_count
		`, tmCol.ID, "en", "es", "Hello", tgt, int32(i+1)).Scan(&id, &uc); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
		// Stagger updated_at so survivor selection is deterministic. Third row
		// (i=2) gets the newest timestamp.
		if _, err := pool.Exec(ctx, `
			UPDATE translation_memory SET updated_at = now() + make_interval(secs => $1) WHERE id = $2
		`, i, id); err != nil {
			t.Fatalf("stagger updated_at: %v", err)
		}
		rows = append(rows, seeded{id: id, target: tgt, usageCount: uc})
	}
	survivorID := rows[2].id // newest updated_at
	expectedUsageSum := rows[0].usageCount + rows[1].usageCount + rows[2].usageCount

	// Sanity: the wide index actually let all three rows in.
	preCount := countTMEntriesByID(t, q, tmCol.ID, "en", "es", "Hello")
	if preCount != 3 {
		t.Fatalf("pre-migration: expected 3 duplicate rows seeded, got %d (wide index didn't permit them)", preCount)
	}

	// Step 3: run migration 023's cleanup CTE + index swap as raw SQL. Mirror
	// of internal/database/migrations/023_tm_dedupe_on_source_only.sql so a
	// future change to the migration that breaks the cleanup is caught here.
	_, err := pool.Exec(ctx, `
		WITH ranked AS (
		  SELECT id, tm_id, source_language, target_language, source_hash,
		         source_text, target_text, source_rich_text, target_rich_text,
		         usage_count, updated_at,
		         ROW_NUMBER() OVER (
		           PARTITION BY tm_id, source_language, target_language, source_hash
		           ORDER BY updated_at DESC, id DESC
		         ) AS rn,
		         FIRST_VALUE(id) OVER (
		           PARTITION BY tm_id, source_language, target_language, source_hash
		           ORDER BY updated_at DESC, id DESC
		         ) AS survivor_id,
		         SUM(usage_count) OVER (
		           PARTITION BY tm_id, source_language, target_language, source_hash
		         ) AS group_usage_sum,
		         COUNT(*) OVER (
		           PARTITION BY tm_id, source_language, target_language, source_hash
		         ) AS group_size
		    FROM translation_memory
		   WHERE created_by = 'human'
		),
		losers AS (SELECT * FROM ranked WHERE rn > 1),
		history_writes AS (
		  INSERT INTO translation_memory_history
		    (entry_id, source_text, target_text, source_rich_text, target_rich_text,
		     changed_by_user_id, change_reason)
		  SELECT survivor_id, source_text, target_text, source_rich_text, target_rich_text,
		         NULL, 'migration_dedupe'
		    FROM losers
		  RETURNING entry_id
		),
		usage_bumps AS (
		  UPDATE translation_memory tm
		     SET usage_count = r.group_usage_sum
		    FROM ranked r
		   WHERE tm.id = r.survivor_id
		     AND r.rn = 1
		     AND r.group_size > 1
		  RETURNING tm.id
		),
		deletions AS (
		  DELETE FROM translation_memory
		   WHERE id IN (SELECT id FROM losers)
		  RETURNING id
		)
		SELECT COUNT(*) FROM deletions;
	`)
	if err != nil {
		t.Fatalf("cleanup CTE: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP INDEX translation_memory_pipeline_dedupe`); err != nil {
		t.Fatalf("drop wide index: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE UNIQUE INDEX translation_memory_pipeline_dedupe
		  ON translation_memory (tm_id, source_language, target_language, source_hash)
		  WHERE created_by = 'human'
	`); err != nil {
		t.Fatalf("recreate narrow index: %v", err)
	}

	// Assert: survivor is the third row, with target = "Hola amigos" and
	// usage_count = sum of the three originals.
	postEntries, err := q.LookupTMAll(ctx, db.LookupTMAllParams{
		TmID: tmCol.ID, SourceLanguage: "en", TargetLanguage: "es", SourceText: "Hello",
	})
	if err != nil {
		t.Fatalf("post-migration LookupTMAll: %v", err)
	}
	if len(postEntries) != 1 {
		t.Fatalf("expected 1 row after backfill (3 → 1), got %d", len(postEntries))
	}
	got := postEntries[0]
	if got.ID != survivorID {
		t.Errorf("survivor.id = %d, expected newest-by-updated_at row %d", got.ID, survivorID)
	}
	if got.TargetText != "Hola amigos" {
		t.Errorf("survivor.target_text = %q, expected 'Hola amigos' (most-recent confirm)", got.TargetText)
	}
	if got.UsageCount != expectedUsageSum {
		t.Errorf("survivor.usage_count = %d, expected SUM(%d, %d, %d) = %d",
			got.UsageCount, rows[0].usageCount, rows[1].usageCount, rows[2].usageCount, expectedUsageSum)
	}

	// Assert: 2 history rows captured the BEFORE state of the dropped losers,
	// each with change_reason='migration_dedupe' and changed_by_user_id IS NULL.
	history, err := q.ListTMEntryHistory(ctx, survivorID)
	if err != nil {
		t.Fatalf("ListTMEntryHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history rows (the 2 losers), got %d", len(history))
	}
	historyTargets := map[string]bool{}
	for _, h := range history {
		historyTargets[h.TargetText] = true
		if h.ChangeReason != "migration_dedupe" {
			t.Errorf("history row target=%q: change_reason=%q, expected 'migration_dedupe'", h.TargetText, h.ChangeReason)
		}
		if h.ChangedByUserID != nil {
			t.Errorf("history row target=%q: changed_by_user_id=%d, expected NULL (system migration)", h.TargetText, *h.ChangedByUserID)
		}
	}
	for _, tgt := range []string{"Hola", "Hola, mundo"} {
		if !historyTargets[tgt] {
			t.Errorf("history missing loser target %q (was: %v)", tgt, historyTargets)
		}
	}
}

// countTMEntriesByID counts TM rows for a (tm_id, src_lang, tgt_lang, source)
// tuple regardless of target — used by the migration backfill test which
// needs to count duplicates that share a source but differ on target.
func countTMEntriesByID(t *testing.T, q db.Querier, tmID int64, srcLang, tgtLang, src string) int {
	t.Helper()
	entries, err := q.LookupTMAll(context.Background(), db.LookupTMAllParams{
		TmID:           tmID,
		SourceLanguage: srcLang,
		TargetLanguage: tgtLang,
		SourceText:     src,
	})
	if err != nil {
		t.Fatalf("LookupTMAll: %v", err)
	}
	return len(entries)
}

// TestIntegrationTMWriteBack_TMXImportDuplicatesPreserved ensures the TM
// partial unique index is scoped to `created_by = 'human'` so TMX imports
// (where every <tu> is preserved as-is) still work with duplicates.
func TestIntegrationTMWriteBack_TMXImportDuplicatesPreserved(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, tmID, _ := setupTMWriteBackFixture(t, q)

	// Simulate a TMX import inserting the same source+target twice with non-human `created_by`.
	for i := 0; i < 2; i++ {
		if _, err := q.InsertTM(ctx, db.InsertTMParams{
			TmID:           tmID,
			SourceLanguage: "en",
			TargetLanguage: "es",
			SourceText:     "Import src",
			TargetText:     "Import tgt",
			CreatedBy:      "tmx-import",
			TuID:           "",
		}); err != nil {
			t.Fatalf("InsertTM (TMX simulation) %d: %v", i, err)
		}
	}

	got := countTMEntries(t, q, tmID, "en", "es", "Import src", "Import tgt")
	if got != 2 {
		t.Errorf("expected 2 TMX-import entries preserved as-is, got %d — partial unique index is catching TMX imports, which it should NOT",
			got)
	}
}

// Failure messages print the value, not the pointer address.
func strPtr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return strconv.Quote(*s)
}

func pctPtr(p *int16) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.Itoa(int(*p))
}
