package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/mt"
)

func TestTranslateFile_AllMT_NoTMAssignments(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World"},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// All segments should go to MT (no project TM assignments)
	if len(mq.updateMTCalls) != 2 {
		t.Fatalf("expected 2 MT updates, got %d", len(mq.updateMTCalls))
	}
	// Engine attribution lives in translation_history.mt_engine_id (asserted
	// via the mt emission in the integration suite). Here we just check the
	// MT text landed in active_text.
	for _, call := range mq.updateMTCalls {
		if call.ActiveText == nil || *call.ActiveText == "" {
			t.Errorf("expected non-empty MT text for translation %d", call.ID)
		}
	}
	// No TM → no human text updates
	if len(mq.updateHumanCalls) != 0 {
		t.Errorf("expected 0 human text updates (no TM), got %d", len(mq.updateHumanCalls))
	}
}

func TestTranslateFile_TMHitSkipsMT(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World"},
	}

	// Project has TM assignment with priority 0
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// TM has a match for "Hello" but not "World"
	mq.tmEntries[tmKey(50, "en", "de", "Hello")] = db.TranslationMemory{
		TmID:           50,
		SourceLanguage: "en",
		TargetLanguage: "de",
		SourceText:     "Hello",
		TargetText:     "Hallo",
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// "Hello" should be filled from TM via UpdateTranslationTMFuzzy (score=1.0) + UpdateTranslationHuman
	// "World" should go to MT via UpdateTranslationMT
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected 1 TM fuzzy update (exact match), got %d", len(mq.updateTMFuzzyCalls))
	}
	tmCall := mq.updateTMFuzzyCalls[0]
	if tmCall.ID != 100 {
		t.Errorf("expected TM match for translation 100, got %d", tmCall.ID)
	}
	if *tmCall.ActiveText != "Hallo" {
		t.Errorf("expected TM text 'Hallo', got %s", *tmCall.ActiveText)
	}
	// The per-segment match percent lives in the history emission
	// (kind='tm', tm_match_percent=100 for exact matches).
	if len(mq.insertHistoryCalls) == 0 {
		t.Fatal("expected at least one translation_history insert for the TM-matched segment")
	}
	tmHist := findHistoryCall(mq.insertHistoryCalls, db.TranslationHistoryKindTm)
	if tmHist == nil {
		t.Fatal("expected a 'tm' history insert; got none")
	}
	if tmHist.TmMatchPercent == nil || *tmHist.TmMatchPercent != 100 {
		t.Errorf("expected tm_match_percent=100 for exact match, got %v", tmHist.TmMatchPercent)
	}

	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update call (for 'World'), got %d", len(mq.updateMTCalls))
	}
	if mq.updateMTCalls[0].ID != 101 {
		t.Errorf("expected MT for translation 101, got %d", mq.updateMTCalls[0].ID)
	}

	// TM hit should also set human_text
	if len(mq.updateHumanCalls) != 1 {
		t.Fatalf("expected 1 human text update (TM match), got %d", len(mq.updateHumanCalls))
	}
	if *mq.updateHumanCalls[0].HumanText != "Hallo" {
		t.Errorf("expected human text 'Hallo', got %s", *mq.updateHumanCalls[0].HumanText)
	}
}

func TestTranslateFile_TMPriorityOrder(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
	}

	// Two TMs assigned, TM 50 (priority 0) and TM 51 (priority 1)
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
		{TmID: 51, Mode: "read_write", Priority: 1},
	}

	// Both TMs have a match, but TM 50 (higher priority = lower number) should win
	mq.tmEntries[tmKey(50, "en", "de", "Hello")] = db.TranslationMemory{
		TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello", TargetText: "Hallo (TM50)",
	}
	mq.tmEntries[tmKey(51, "en", "de", "Hello")] = db.TranslationMemory{
		TmID: 51, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Hello", TargetText: "Hallo (TM51)",
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Exact TM match goes through UpdateTranslationTMFuzzy with score=1.0
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected 1 TM fuzzy update (exact match), got %d", len(mq.updateTMFuzzyCalls))
	}
	if *mq.updateTMFuzzyCalls[0].ActiveText != "Hallo (TM50)" {
		t.Errorf("expected TM50 to win by priority, got %s", *mq.updateTMFuzzyCalls[0].ActiveText)
	}
}

func TestTranslateFile_FuzzyTMMatch(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	// Set project with low threshold to accept 71% fuzzy match
	mq.setProjectFuzzyThreshold(projectID, 0.6)
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Link to reset your password."},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Something completely different"},
	}

	// TM assignment
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// No exact TM match for either segment.
	// Fuzzy match: "Link to reset your password." matches "Reset your password." at 71%
	mq.fuzzyTMEntries["50:en:de:Link to reset your password."] = []db.FuzzyLookupTMRow{
		{
			ID:             1,
			TmID:           50,
			SourceLanguage: "en",
			TargetLanguage: "de",
			SourceText:     "Reset your password.",
			TargetText:     "Setzen Sie Ihr Passwort zurück.",
			Score:          0.71,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// ID 100: fuzzy TM match → UpdateTranslationTMFuzzy with score < 1.0
	// ID 101: no TM match → goes to MT
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected 1 fuzzy TM update, got %d", len(mq.updateTMFuzzyCalls))
	}
	fuzzyCall := mq.updateTMFuzzyCalls[0]
	if fuzzyCall.ID != 100 {
		t.Errorf("expected fuzzy match for ID 100, got %d", fuzzyCall.ID)
	}
	// Lev 1 - 9/28 ≈ 0.6786 → 68 (rounded, matching the TM sidebar). Pinned
	// exactly so a regression that re-derives from hit.Score (giving the
	// mock's 71) fires. Rounds up from 67.86, not truncates to 67 — see
	// translationmemory.ScoreToPercent.
	tmHist := findHistoryCall(mq.insertHistoryCalls, db.TranslationHistoryKindTm)
	if tmHist == nil {
		t.Fatal("expected a 'tm' history insert for the fuzzy match")
	}
	if tmHist.TmMatchPercent == nil || *tmHist.TmMatchPercent != 68 {
		t.Errorf("expected tm_match_percent=68 (Levenshtein, rounded), got %v", tmHist.TmMatchPercent)
	}
	if fuzzyCall.ActiveText == nil || *fuzzyCall.ActiveText != "Setzen Sie Ihr Passwort zurück." {
		t.Errorf("expected fuzzy TM target text, got %v", fuzzyCall.ActiveText)
	}
	// TM provenance is the history row's origin='tm' (asserted via tmHist
	// above).

	// Fuzzy match should NOT set human_text (only exact matches auto-approve)
	if len(mq.updateHumanCalls) != 0 {
		t.Errorf("expected 0 human text updates for fuzzy match, got %d", len(mq.updateHumanCalls))
	}

	// ID 101 should go to MT
	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update (for 'Something completely different'), got %d", len(mq.updateMTCalls))
	}
	if mq.updateMTCalls[0].ID != 101 {
		t.Errorf("expected MT for ID 101, got %d", mq.updateMTCalls[0].ID)
	}
}

// Anti-tautology: reverting bestRescoredCandidate to "first wins" or
// "highest hit.Score wins" flips this RED.
func TestTranslateFile_FuzzyPicksLevenshteinWinnerNotPgTrgmWinner(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.setProjectFuzzyThreshold(projectID, 0.6)
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello world"},
	}
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// Mock Score field ranks A > B; Levenshtein flips it:
	//   A: "Hello there world" vs "Hello world" → 1 - 6/17 ≈ 0.647
	//   B: "Hello world!"      vs "Hello world" → 1 - 1/12 ≈ 0.917
	mq.fuzzyTMEntries["50:en:de:Hello world"] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Hello there world", TargetText: "WRONG-pg_trgm-winner",
			Score: 0.95,
		},
		{
			ID: 2, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Hello world!", TargetText: "RIGHT-lev-winner",
			Score: 0.70,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline); err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected 1 fuzzy update, got %d", len(mq.updateTMFuzzyCalls))
	}
	got := mq.updateTMFuzzyCalls[0].ActiveText
	if got == nil || *got != "RIGHT-lev-winner" {
		t.Errorf("expected Levenshtein winner (B), got %v", got)
	}
}

func TestTranslateFile_ExactTMTakesPriorityOverFuzzy(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Reset your password."},
	}

	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// Exact match exists
	mq.tmEntries[tmKey(50, "en", "de", "Reset your password.")] = db.TranslationMemory{
		TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Reset your password.", TargetText: "Passwort zurücksetzen.",
	}

	// Fuzzy match also exists (should NOT be used since exact wins)
	mq.fuzzyTMEntries["50:en:de:Reset your password."] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Reset your password.", TargetText: "Passwort zurücksetzen.",
			Score: 1.0,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Should use exact match path (score=1.0) and set human_text
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected 1 TM fuzzy update (exact path), got %d", len(mq.updateTMFuzzyCalls))
	}
	// Exact match → tm_match_percent=100 in history.
	tmHist3 := findHistoryCall(mq.insertHistoryCalls, db.TranslationHistoryKindTm)
	if tmHist3 == nil || tmHist3.TmMatchPercent == nil || *tmHist3.TmMatchPercent != 100 {
		t.Errorf("expected tm history percent=100 for exact match, got %v", tmHist3)
	}
	if len(mq.updateHumanCalls) != 1 {
		t.Fatalf("expected 1 human text update (exact match auto-approves), got %d", len(mq.updateHumanCalls))
	}
	// No MT calls
	if len(mq.updateMTCalls) != 0 {
		t.Errorf("expected 0 MT calls (exact TM match), got %d", len(mq.updateMTCalls))
	}
}

func TestTranslateFile_FuzzyBelowThreshold_GoesToMT(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Completely unrelated text"},
	}

	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// Fuzzy entries exist but score is below 0.75 default threshold
	mq.fuzzyTMEntries["50:en:de:Completely unrelated text"] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Reset your password.", TargetText: "Passwort zurücksetzen.",
			Score: 0.3, // below threshold
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Below threshold → no fuzzy match, goes to MT
	if len(mq.updateTMFuzzyCalls) != 0 {
		t.Errorf("expected 0 fuzzy TM updates (below threshold), got %d", len(mq.updateTMFuzzyCalls))
	}
	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update, got %d", len(mq.updateMTCalls))
	}
}

func TestTranslateFile_FuzzyDisabled_ThresholdOne(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	// Set threshold to 1.0 = exact matches only
	mq.setProjectFuzzyThreshold(projectID, 1.0)
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Link to reset your password."},
	}

	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// Fuzzy match exists with high score, but threshold=1.0 disables fuzzy
	mq.fuzzyTMEntries["50:en:de:Link to reset your password."] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Reset your password.", TargetText: "Passwort zurücksetzen.",
			Score: 0.95,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Fuzzy disabled → should go to MT
	if len(mq.updateTMFuzzyCalls) != 0 {
		t.Errorf("expected 0 fuzzy TM updates (threshold=1.0 disables fuzzy), got %d", len(mq.updateTMFuzzyCalls))
	}
	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update, got %d", len(mq.updateMTCalls))
	}
}

func TestTranslateFile_CustomThreshold(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	// The project's fuzzy_threshold filters AFTER the
	// Scorer rescores stage-1 candidates. Pick a threshold that the
	// rescored Levenshtein percent for this pair must miss:
	// "Hello world" vs "Hello world there" → dist 6 / max 17 ≈ 0.647.
	// 0.85 rejects it; lower thresholds would accept it.
	mq.setProjectFuzzyThreshold(projectID, 0.85)
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello world"},
	}

	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}

	// Mock pg_trgm Score field is a fallback used only when the
	// Scorer declines (over the 2KB cap). For this test the Scorer
	// rescores the pair to ~0.65, which is below the 0.85 threshold.
	mq.fuzzyTMEntries["50:en:de:Hello world"] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Hello world there", TargetText: "Hallo Welt da",
			Score: 0.6,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	if len(mq.updateTMFuzzyCalls) != 0 {
		t.Errorf("expected 0 fuzzy TM updates (Levenshtein ≈ 0.65 < 0.85 threshold), got %d", len(mq.updateTMFuzzyCalls))
	}
	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update, got %d", len(mq.updateMTCalls))
	}
}

func TestTranslateFile_ZeroThresholdIsHonored(t *testing.T) {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	mq.setProjectFuzzyThreshold(projectID, 0)
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello world"},
	}
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
	}
	mq.fuzzyTMEntries["50:en:de:Hello world"] = []db.FuzzyLookupTMRow{
		{
			ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
			SourceText: "Hello world there", TargetText: "Hallo Welt da", Score: 0.6,
		},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline); err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected zero threshold to accept the fuzzy TM match, got %d updates", len(mq.updateTMFuzzyCalls))
	}
	if len(mq.updateMTCalls) != 0 {
		t.Fatalf("expected no MT fallback after fuzzy match, got %d updates", len(mq.updateMTCalls))
	}
}

func TestTranslateFile_FiltersTargetLanguage(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de", "fr"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
		{ID: 101, SegmentID: 2, TargetLanguage: "fr", SourceText: "Hello"},
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)

	// Only translate German
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Should only have MT'd the German translation
	if len(mq.updateMTCalls) != 1 {
		t.Fatalf("expected 1 MT update (only 'de'), got %d", len(mq.updateMTCalls))
	}
	if mq.updateMTCalls[0].ID != 100 {
		t.Errorf("expected translation 100 (de), got %d", mq.updateMTCalls[0].ID)
	}
}

func TestTranslateFile_EmptyRows_NoOp(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	// No untranslated rows

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}
	if len(mq.updateMTCalls) != 0 {
		t.Errorf("expected 0 MT updates, got %d", len(mq.updateMTCalls))
	}
}

// --- Cooperative cancellation: pipeline aborts when context is cancelled mid-chunk ---

func TestTranslateFile_AbortsOnContextCancel(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	// 200 segments → several mtChunkSize chunks
	rows := make([]db.ListUntranslatedByFileRow, 200)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	// Cancel context after the first chunk completes
	ctx, cancel := context.WithCancel(context.Background())
	var chunkCount int
	cancelAfterFirstChunk := func(phase string, done, total int) {
		chunkCount++
		if chunkCount >= 1 {
			cancel() // cancel after first chunk
		}
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(ctx, int64(1), 1, "en", "de", cancelAfterFirstChunk, FullPipeline)

	// Should fail with context cancelled error
	if err == nil {
		t.Fatal("expected error when context cancelled, got nil")
	}

	// First chunk (mtChunkSize rows) should be saved
	if len(mq.updateMTCalls) != 30 {
		t.Errorf("expected 30 MT updates (first chunk saved before cancel), got %d", len(mq.updateMTCalls))
	}
}

// gappyEngine leaves one segment untranslated, the way a batched LLM reply
// that omitted a segment does after its retry also came back empty
type gappyEngine struct{}

func (gappyEngine) Name() string { return "gappy" }
func (gappyEngine) Translate(_ context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	out := make([]mt.TranslateResult, len(req.Texts))
	for i, tx := range req.Texts {
		if tx == "Skipped" {
			continue // no text for this one
		}
		out[i] = mt.TranslateResult{Text: "T:" + tx}
	}
	return out, nil
}

// A segment the engine didn't answer for must be left untranslated rather than
// written as blank text — a blank active_text would mark it done and hide it
// from the next MT run and from the linguist.
func TestTranslateFile_MTSkipsEmptyResults(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Skipped"},
		{ID: 102, SegmentID: 3, TargetLanguage: "de", SourceText: "World"},
	}

	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return gappyEngine{}, nil })
	outcome, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if len(mq.updateMTCalls) != 2 {
		t.Fatalf("expected only the 2 answered segments written, got %d updates", len(mq.updateMTCalls))
	}
	// The History count is what was written, not what was sent.
	if outcome.MTTranslated != 2 {
		t.Errorf("outcome.MTTranslated = %d, want the 2 written segments", outcome.MTTranslated)
	}
	for _, call := range mq.updateMTCalls {
		if call.ID == 101 {
			t.Errorf("the unanswered segment must not be written at all, got %+v", call)
		}
		if call.ActiveText == nil || *call.ActiveText == "" {
			t.Errorf("no blank active_text may be written, got %+v", call)
		}
	}
}

// --- Edge cases: boundary conditions for chunking ---

func TestTranslateFile_ExactlyOneChunk(t *testing.T) {
	// Exactly mtChunkSize segments → 1 engine call, 1 progress callback
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	rows := make([]db.ListUntranslatedByFileRow, mtChunkSize)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	engine := &callCountEngine{inner: mt.Mock{}}
	var progressCalls int
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", func(phase string, done, total int) {
		progressCalls++
		if phase == "mt" && (done != mtChunkSize || total != mtChunkSize) {
			t.Errorf("expected MT progress (%d, %d), got (%d, %d)", mtChunkSize, mtChunkSize, done, total)
		}
	}, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}
	if engine.callCount != 1 {
		t.Errorf("expected exactly 1 engine call for %d segments, got %d", mtChunkSize, engine.callCount)
	}
	if progressCalls != 1 {
		t.Errorf("expected 1 progress callback, got %d", progressCalls)
	}
	if len(mq.updateMTCalls) != mtChunkSize {
		t.Errorf("expected %d MT updates, got %d", mtChunkSize, len(mq.updateMTCalls))
	}
}

func TestTranslateFile_SingleSegment(t *testing.T) {
	// 1 segment → 1 engine call with batch size 1, 1 progress callback
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
	}

	engine := &callCountEngine{inner: mt.Mock{}}
	var progressCalls int
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", func(phase string, done, total int) {
		progressCalls++
		if phase == "mt" && (done != 1 || total != 1) {
			t.Errorf("expected MT progress (1, 1), got (%d, %d)", done, total)
		}
	}, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}
	if engine.callCount != 1 {
		t.Errorf("expected 1 engine call, got %d", engine.callCount)
	}
	if engine.lastBatch != 1 {
		t.Errorf("expected batch size 1, got %d", engine.lastBatch)
	}
	if progressCalls != 1 {
		t.Errorf("expected 1 progress callback, got %d", progressCalls)
	}
}

func TestTranslateFile_ChunkSizePlusOne(t *testing.T) {
	// mtChunkSize+1 segments → 2 engine calls (mtChunkSize + 1)
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	rows := make([]db.ListUntranslatedByFileRow, mtChunkSize+1)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	engine := &callCountEngine{inner: mt.Mock{}}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}
	if engine.callCount != 2 {
		t.Errorf("expected 2 engine calls for %d segments, got %d", mtChunkSize+1, engine.callCount)
	}
	if engine.lastBatch != 1 {
		t.Errorf("expected last batch size 1, got %d", engine.lastBatch)
	}
	if len(mq.updateMTCalls) != mtChunkSize+1 {
		t.Errorf("expected %d MT updates, got %d", mtChunkSize+1, len(mq.updateMTCalls))
	}
}

// --- Chunking sends correct number of batches ---

// callCountEngine wraps an mt.Engine and counts Translate calls.
type callCountEngine struct {
	inner     mt.Engine
	callCount int
	lastBatch int
}

func (e *callCountEngine) Name() string { return e.inner.Name() }
func (e *callCountEngine) Translate(ctx context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	e.callCount++
	e.lastBatch = len(req.Texts)
	return e.inner.Translate(ctx, req)
}

func TestTranslateFile_ChunkedMT_CorrectBatchCount(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	// Create 100 untranslated segments (more than one mtChunkSize chunk)
	rows := make([]db.ListUntranslatedByFileRow, 100)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	engine := &callCountEngine{inner: mt.Mock{}}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// All 100 segments should be saved
	if len(mq.updateMTCalls) != 100 {
		t.Errorf("expected 100 MT updates, got %d", len(mq.updateMTCalls))
	}

	// Engine should be called in chunks (more than one call for 100 segments)
	if engine.callCount < 2 {
		t.Errorf("expected at least 2 engine calls (chunked), got %d", engine.callCount)
	}
}

// Partial failure — first chunk saves, second fails

// failAfterNEngine succeeds for the first N Translate calls, then returns err
// ("engine overloaded" when nil).
type failAfterNEngine struct {
	inner      mt.Engine
	callCount  int
	failAfterN int
	err        error
}

func (e *failAfterNEngine) Name() string { return e.inner.Name() }
func (e *failAfterNEngine) Translate(ctx context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	e.callCount++
	if e.callCount > e.failAfterN {
		if e.err != nil {
			return nil, e.err
		}
		return nil, fmt.Errorf("engine overloaded")
	}
	return e.inner.Translate(ctx, req)
}

func TestTranslateFile_ChunkedMT_PartialFailure(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	// 100 segments
	rows := make([]db.ListUntranslatedByFileRow, 100)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	engine := &failAfterNEngine{inner: mt.Mock{}, failAfterN: 1}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	outcome, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)

	// Should fail (second chunk errors)
	if err == nil {
		t.Fatal("expected error from partial failure, got nil")
	}
	if !IsMTFailure(err) {
		t.Errorf("an engine error must be marked as an MT failure, got %v", err)
	}
	if _, err := svc.TranslateFile(context.Background(), int64(1), 404, "en", "de", nil, FullPipeline); err == nil || IsMTFailure(err) {
		t.Errorf("a failure before the MT step must not be marked as one, got %v", err)
	}

	// First chunk (mtChunkSize rows) should be saved to DB
	if len(mq.updateMTCalls) != 30 {
		t.Errorf("expected 30 MT updates (first chunk saved), got %d", len(mq.updateMTCalls))
	}
	// The failed attempt still reports what it saved: a retry adds its own
	// count to this one.
	if outcome.MTTranslated != 30 || outcome.MTEngine != "mock" {
		t.Errorf("outcome = %d segments by %q after a failure, want the 30 saved segments by mock", outcome.MTTranslated, outcome.MTEngine)
	}
}

// A provider error partway (rate limited, 5xx) is the usual reason a run is
// retried. It becomes a message the user can act on, and the outcome still
// carries what was saved before it.
func TestTranslateFile_ChunkedMT_ProviderFailureKeepsTheSavedCount(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	rows := make([]db.ListUntranslatedByFileRow, 100)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	engine := &failAfterNEngine{inner: mt.Mock{}, failAfterN: 1,
		err: &ai.ProviderError{Provider: ai.ProviderOpenAI, Status: 503, Kind: ai.ProviderErrProvider}}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	outcome, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)

	var clientErr *httputil.ClientError
	if !IsMTFailure(err) || !errors.As(err, &clientErr) {
		t.Fatalf("err = %v, want an MT failure carrying the provider message", err)
	}
	if outcome.MTTranslated != 30 || outcome.MTEngine != "mock" {
		t.Errorf("outcome = %d segments by %q after a provider failure, want the 30 saved segments by mock", outcome.MTTranslated, outcome.MTEngine)
	}
}

// Progress callback called with correct counts

func TestTranslateFile_ChunkedMT_ProgressCallback(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})

	// 75 segments → chunks of mtChunkSize: 30 + 30 + 15
	rows := make([]db.ListUntranslatedByFileRow, 75)
	for i := range rows {
		rows[i] = db.ListUntranslatedByFileRow{
			ID: int64(100 + i), SegmentID: int64(i + 1), TargetLanguage: "de",
			SourceText: fmt.Sprintf("Sentence %d", i),
		}
	}
	mq.untranslated[1] = rows

	var progressCalls []struct {
		phase       string
		done, total int
	}
	onProgress := func(phase string, done, total int) {
		progressCalls = append(progressCalls, struct {
			phase       string
			done, total int
		}{phase, done, total})
	}

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", onProgress, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile returned error: %v", err)
	}

	// Should have one progress call per chunk: (30, 75) … (75, 75)
	if len(progressCalls) < 2 {
		t.Fatalf("expected at least 2 progress callbacks, got %d", len(progressCalls))
	}

	// Find the MT progress calls (skip TM phase)
	var mtCalls []struct {
		phase       string
		done, total int
	}
	for _, c := range progressCalls {
		if c.phase == "mt" {
			mtCalls = append(mtCalls, c)
		}
	}

	if len(mtCalls) < 2 {
		t.Fatalf("expected at least 2 MT progress callbacks, got %d (all calls: %v)", len(mtCalls), progressCalls)
	}

	first := mtCalls[0]
	if first.done != 30 || first.total != 75 {
		t.Errorf("first MT progress: expected (30, 75), got (%d, %d)", first.done, first.total)
	}

	last := mtCalls[len(mtCalls)-1]
	if last.done != 75 || last.total != 75 {
		t.Errorf("last MT progress: expected (75, 75), got (%d, %d)", last.done, last.total)
	}
}

// TestTranslateFile_BatchTMReducesQueryCount verifies the batch optimization:
// with 100 segments and 1 TM, the pipeline should use LookupTMBatch (few batch calls)
// instead of LookupTM (100 per-segment calls).
func TestTranslateFile_BatchTMReducesQueryCount(t *testing.T) {
	mq := newMockQuerier()

	// Create a file with 100 untranslated segments
	mq.files[1] = db.File{ID: 1, ProjectID: 1}
	mq.projects[1] = db.Project{ID: 1, TenantID: 1, SettingsProfileID: 1}
	mq.settingsProfiles[1] = db.SettingsProfile{ID: 1, FuzzyThreshold: 0.75}

	// Assign 1 TM
	mq.settingsTMs[langKey(1, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 10},
	}
	// Seed a Mock MT router so segments without TM matches go through MT
	// (no MT router = MT phase skipped).
	mq.addMTRouter(1, 1, "mock")

	// Create 100 segments, seed 20 TM exact matches
	var rows []db.ListUntranslatedByFileRow
	for i := 0; i < 100; i++ {
		src := fmt.Sprintf("Segment number %d for batch test", i)
		rows = append(rows, db.ListUntranslatedByFileRow{
			ID: int64(i + 1), SourceText: src, TargetLanguage: "de",
		})
		// Every 5th segment gets a TM match (20 matches total)
		if i%5 == 0 {
			key := tmKey(10, "en", "de", src)
			mq.tmEntries[key] = db.TranslationMemory{
				ID: int64(1000 + i), TmID: 10,
				SourceLanguage: "en", TargetLanguage: "de",
				SourceText: src, TargetText: fmt.Sprintf("Übersetzung %d", i),
			}
		}
	}
	mq.untranslated[1] = rows

	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline)
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	// CORE ASSERTION: batch path was used, per-segment path was NOT
	if mq.lookupTMBatchCalls == 0 {
		t.Error("expected LookupTMBatch to be called (batch path), but it was not called")
	}
	if mq.lookupTMCalls > 0 {
		t.Errorf("expected LookupTM NOT to be called (old per-segment path), but it was called %d times", mq.lookupTMCalls)
	}

	// Batch calls should be ceil(100/500) = 1 batch (100 segments < 500 batch size)
	if mq.lookupTMBatchCalls != 1 {
		t.Errorf("expected 1 batch call for 100 segments, got %d", mq.lookupTMBatchCalls)
	}

	// 20 exact matches → 80 segments go to MT
	if len(mq.updateMTCalls) != 80 {
		t.Errorf("expected 80 MT updates (100 - 20 TM matches), got %d", len(mq.updateMTCalls))
	}

	// 20 TM matches should have been applied
	if len(mq.updateTMFuzzyCalls) != 20 {
		t.Errorf("expected 20 TM match updates, got %d", len(mq.updateTMFuzzyCalls))
	}
}

// ─── Engine-native tag handling vs TransferTags fallback ──────────

// fakeTaggedEngine implements mt.TagPreservingEngine. TranslateTagged returns
// a fixed, deliberately-distinctive tagged string (it wraps the FIRST word —
// something TransferTags would never produce, since Tier 1 word-index would
// wrap the translated word). Translate (plain path) prefixes "PLAIN:" so a
// plain row is unmistakable. plainCalls / taggedCalls record routing.
type fakeTaggedEngine struct {
	taggedOut   string
	plainCalls  int
	taggedCalls int
}

func (e *fakeTaggedEngine) Name() string { return "fake-tagged" }

func (e *fakeTaggedEngine) Translate(_ context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	e.plainCalls++
	out := make([]mt.TranslateResult, len(req.Texts))
	for i, t := range req.Texts {
		out[i] = mt.TranslateResult{Text: "PLAIN:" + t}
	}
	return out, nil
}

func (e *fakeTaggedEngine) TranslateTagged(_ context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	e.taggedCalls++
	out := make([]mt.TranslateResult, len(req.Texts))
	for i := range req.Texts {
		out[i] = mt.TranslateResult{Text: e.taggedOut}
	}
	return out, nil
}

func mtCallByID(calls []db.UpdateTranslationMTParams, id int64) *db.UpdateTranslationMTParams {
	for i := range calls {
		if calls[i].ID == id {
			return &calls[i]
		}
	}
	return nil
}

func strDeref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestTranslateFile_Tier2_EngineHandlesTags — when the engine is tag-aware,
// segments WITH source_rich_text go through TranslateTagged and the engine's
// repositioned output is stored verbatim as active_rich_text (NO TransferTags
// projection). Segments WITHOUT tags still go through plain Translate.
func TestTranslateFile_Tier2_EngineHandlesTags(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"es"})
	richSrc := `This text is in <g id="1" ctype="bold">bold</g>`
	// Engine output wraps the FIRST word — Tier 1 word-index would wrap the
	// last word ("negrita"), so this placement is only reachable via Tier 2.
	engineOut := `<g id="1" ctype="bold">Este</g> texto está en negrita`
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "es", SourceText: "This text is in bold", SourceRichText: &richSrc},
		{ID: 101, SegmentID: 2, TargetLanguage: "es", SourceText: "Plain segment"},
	}

	engine := &fakeTaggedEngine{taggedOut: engineOut}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "es", nil, TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	// Routing: the rich row went through TranslateTagged, the plain row through Translate.
	if engine.taggedCalls != 1 {
		t.Errorf("expected 1 TranslateTagged call (rich row), got %d", engine.taggedCalls)
	}
	if engine.plainCalls != 1 {
		t.Errorf("expected 1 plain Translate call (plain row), got %d", engine.plainCalls)
	}

	// Rich row: active_rich_text is the engine output verbatim; active_text is stripped.
	rich := mtCallByID(mq.updateMTCalls, 100)
	if rich == nil {
		t.Fatal("no MT update for rich row (id=100)")
	}
	if strDeref(rich.ActiveRichText) != engineOut {
		t.Errorf("rich active_rich_text = %q, want engine output %q (Tier 2 = verbatim, not TransferTags)", strDeref(rich.ActiveRichText), engineOut)
	}
	if strDeref(rich.ActiveText) != "Este texto está en negrita" {
		t.Errorf("rich active_text = %q, want stripped plain %q", strDeref(rich.ActiveText), "Este texto está en negrita")
	}

	// Plain row: no rich text, plain Translate output.
	plain := mtCallByID(mq.updateMTCalls, 101)
	if plain == nil {
		t.Fatal("no MT update for plain row (id=101)")
	}
	if plain.ActiveRichText != nil {
		t.Errorf("plain row active_rich_text = %q, want nil", strDeref(plain.ActiveRichText))
	}
	if strDeref(plain.ActiveText) != "PLAIN:Plain segment" {
		t.Errorf("plain active_text = %q, want %q", strDeref(plain.ActiveText), "PLAIN:Plain segment")
	}
}

// TestTranslateFile_Tier1_FallbackWhenEngineNotTagAware — a plain (non
// tag-aware) engine must NOT receive rich text; instead TransferTags (Tier 1)
// projects the source tags onto the plain MT output. mt.Mock prefixes "[ES] "
// so the wrapped word survives verbatim and lands correctly.
func TestTranslateFile_Tier1_FallbackWhenEngineNotTagAware(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"es"})
	richSrc := `This text is in <g id="1" ctype="bold">bold</g>`
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "es", SourceText: "This text is in bold", SourceRichText: &richSrc},
	}

	// mt.Mock is NOT a TagPreservingEngine → Tier 1 path.
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return mt.Mock{}, nil })
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "es", nil, TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	call := mtCallByID(mq.updateMTCalls, 100)
	if call == nil {
		t.Fatal("no MT update for rich row (id=100)")
	}
	// mt.Mock output is "[ES] This text is in bold"; Tier 1 projects the <g>
	// onto it. Compare against TransferTags directly so the test tracks the
	// algorithm rather than hard-coding.
	wantRich := TransferTags(richSrc, "This text is in bold", "[ES] This text is in bold")
	if wantRich == "" {
		t.Fatal("TransferTags returned empty — test setup wrong")
	}
	if strDeref(call.ActiveRichText) != wantRich {
		t.Errorf("active_rich_text = %q, want Tier-1 projection %q", strDeref(call.ActiveRichText), wantRich)
	}
	if strDeref(call.ActiveText) != "[ES] This text is in bold" {
		t.Errorf("active_text = %q, want plain MT %q", strDeref(call.ActiveText), "[ES] This text is in bold")
	}
}

// recordingEngine captures each Translate request so a test can assert on the
// (resolved) locales the engine actually sees. Returns one [LANG] prefix per
// text so the pipeline still completes a save.
type recordingEngine struct {
	name     string
	requests []mt.TranslateRequest
}

func (e *recordingEngine) Name() string { return e.name }
func (e *recordingEngine) Translate(_ context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	e.requests = append(e.requests, req)
	results := make([]mt.TranslateResult, len(req.Texts))
	for i, t := range req.Texts {
		results[i] = mt.TranslateResult{Text: "[" + req.TargetLang + "] " + t}
	}
	return results, nil
}

// TestTranslateFile_LibreTranslate_BothLocalesFallToBase covers the regression
// where en-us → de-de hit LibreTranslate as en-us → de (only the target got
// base-code resolution). Wraps a "libretranslate"-named fake so the catalog
// used by mt.ResolveLocalePair is the libretranslate one (which lacks regional
// variants). Asserts the request the engine receives is en → de.
func TestTranslateFile_LibreTranslate_BothLocalesFallToBase(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en-us", []string{"de-de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de-de", SourceText: "Hello"},
	}

	engine := &recordingEngine{name: "libretranslate"}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en-us", "de-de", nil, TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	if len(engine.requests) != 1 {
		t.Fatalf("expected 1 engine request, got %d", len(engine.requests))
	}
	req := engine.requests[0]
	if req.SourceLang != "en" || req.TargetLang != "de" {
		t.Errorf("engine got %s→%s, want en→de (both sides resolved against libretranslate catalog)",
			req.SourceLang, req.TargetLang)
	}
}

// findHistoryCall returns the first InsertTranslationHistoryParams of the
// given kind recorded by the mock querier, or nil if none. Tests use it to
// assert tm_match_percent on the history emission that pairs with a TM fill.
func findHistoryCall(calls []db.InsertTranslationHistoryParams, kind db.TranslationHistoryKind) *db.InsertTranslationHistoryParams {
	for i := range calls {
		if calls[i].Kind == kind {
			return &calls[i]
		}
	}
	return nil
}

// catalogStub is an engine that publishes its own language catalog: en↔de only,
// and it records whether a translate call ever arrives. Each test needs its own
// server because the catalog cache is keyed by endpoint.
func catalogStub(t *testing.T) (mt.Engine, *atomic.Int32) {
	t.Helper()
	var translateCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/languages") {
			fmt.Fprint(w, `[{"code":"en","name":"English","targets":["de"]},{"code":"de","name":"German","targets":["en"]}]`)
			return
		}
		translateCalls.Add(1)
		fmt.Fprint(w, `{"translatedText":["hallo"]}`)
	}))
	t.Cleanup(srv.Close)
	return mt.LibreTranslate{APIURL: srv.URL}, &translateCalls
}

// An engine's own catalog decides which pairs it can take. When it does not
// list one, the phase says so in the user's terms instead of sending the call
// and letting the vendor answer with its own wording — or, worse, succeeding
// against a passthrough code the vendor happens to tolerate.
func TestTranslateFile_UnsupportedPairFailsWithAClearMessage(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"th"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "th", SourceText: "Hello"},
	}

	engine, translateCalls := catalogStub(t)
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })

	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "th", nil, TranslatePhases{UseMT: true})
	if err == nil {
		t.Fatal("expected the MT phase to fail on a pair the engine does not list")
	}
	var clientErr *httputil.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("want a client-facing error, got %T: %v", err, err)
	}
	// Not just "th": the message contains "the", so a bare substring check passes
	// with both codes stripped. The pair is the part worth pinning — the engine
	// name in the message is the type, not the row's name.
	if !strings.Contains(clientErr.Message, "en \u2192 th") {
		t.Errorf("message should name the pair: %q", clientErr.Message)
	}
	if got := translateCalls.Load(); got != 0 {
		t.Errorf("engine must not be called for a pair it does not list, got %d call(s)", got)
	}
	// The catalog gives the same answer on every attempt, so the worker must
	// not retry it.
	if !IsPermanent(err) {
		t.Errorf("a refused pair must be marked permanent, got %T: %v", err, err)
	}
}

// The control: a pair the catalog does cover still runs, so the guard cannot be
// passing by refusing everything.
func TestTranslateFile_SupportedPairStillRuns(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
	}

	engine, translateCalls := catalogStub(t)
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })

	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if got := translateCalls.Load(); got != 1 {
		t.Fatalf("expected the engine to be called once, got %d", got)
	}
}

// An LLM translation engine publishes no catalog, so the mock table stands in
// for locale resolution. That table is a test fixture missing Thai, Hindi,
// Vietnamese and a dozen more — refusing a pair on its authority would narrow
// LLM engines from "any pair" to 35 languages.
func TestTranslateFile_LLMEngineTakesAnyPair(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"th"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "th", SourceText: "Hello"},
	}

	var got mt.TranslateRequest
	llm := mt.LLMEngine{EngineName: "ai:Acme Translator", Fn: func(_ context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
		got = req
		return []mt.TranslateResult{{Text: "สวัสดี"}}, nil
	}}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return llm, nil })

	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "th", nil, TranslatePhases{UseMT: true}); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	if got.TargetLang != "th" {
		t.Errorf("engine got target %q, want th passed through untouched", got.TargetLang)
	}
}

// A vendor we cannot reach has not refused anything: its catalog is our
// compiled-in guess, and a language it added since we wrote that table must not
// be rejected on its behalf.
func TestTranslateFile_UnreachableEngineDoesNotRefuse(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"th"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "th", SourceText: "Hello"},
	}

	// A DeepL pointed at a dead endpoint: the fetch fails, so the catalog is the
	// static table — which has no Thai.
	engine := mt.DeepL{APIKey: "k", BaseURL: "http://127.0.0.1:1"}
	svc := newService(mq, &passthroughTxRunner{q: mq}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })

	_, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "th", nil, TranslatePhases{UseMT: true})
	if err == nil {
		t.Fatal("TranslateFile succeeded against a dead endpoint; want a retryable connection error")
	}
	if msg, ok := httputil.AsClientError(err); ok && strings.Contains(msg, "does not translate") {
		t.Errorf("refused on a fallback catalog the engine never published: %q", msg)
	}
	// An engine that is down now may answer on the next attempt.
	if IsPermanent(err) {
		t.Errorf("an unreachable engine must stay retryable, got a permanent error: %v", err)
	}
}
