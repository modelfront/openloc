package pipeline

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/settings"
)

// Two TMs, one segment: TM 50 (first) holds only a fuzzy match, TM 51
// (second) holds an exact one. The two lookup orders disagree about exactly
// this case.
func newLookupOrderFixture(lookupOrder string) *mockQuerier {
	projectID := int64(10)
	mq := newMockQuerier()
	mq.addFileProject(1, projectID, nil, "en", []string{"de"})
	sp := mq.settingsProfiles[projectID]
	sp.TmLookupOrder = lookupOrder
	mq.settingsProfiles[projectID] = sp
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Reset your password"},
	}
	mq.settingsTMs[langKey(projectID, "en", "de")] = []db.LookupSettingsTMsRow{
		{TmID: 50, Mode: "read_write", Priority: 0},
		{TmID: 51, Mode: "read_write", Priority: 1},
	}
	mq.fuzzyTMEntries["50:en:de:Reset your password"] = []db.FuzzyLookupTMRow{{
		ID: 1, TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Reset your password!", TargetText: "Fuzzy from TM50", Score: 0.9,
	}}
	mq.tmEntries[tmKey(51, "en", "de", "Reset your password")] = db.TranslationMemory{
		TmID: 51, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Reset your password", TargetText: "Exact from TM51",
	}
	return mq
}

func runLookupOrder(t *testing.T, mq *mockQuerier) {
	t.Helper()
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", nil, FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
}

func onlyFill(t *testing.T, mq *mockQuerier) string {
	t.Helper()
	if len(mq.updateTMFuzzyCalls) != 1 {
		t.Fatalf("expected exactly 1 TM fill, got %d", len(mq.updateTMFuzzyCalls))
	}
	return *mq.updateTMFuzzyCalls[0].ActiveText
}

func TestTMLookupOrder_MatchPercent_LaterExactBeatsEarlierFuzzy(t *testing.T) {
	mq := newLookupOrderFixture(settings.TMLookupOrderMatchPercent)
	runLookupOrder(t, mq)

	if got := onlyFill(t, mq); got != "Exact from TM51" {
		t.Errorf("match_percent: expected TM51's exact match, got %q", got)
	}
	if len(mq.updateHumanCalls) != 1 {
		t.Errorf("an exact match is auto-approved; expected 1 human update, got %d", len(mq.updateHumanCalls))
	}
}

func TestTMLookupOrder_TMOrder_EarlierFuzzyBeatsLaterExact(t *testing.T) {
	mq := newLookupOrderFixture(settings.TMLookupOrderTMOrder)
	runLookupOrder(t, mq)

	if got := onlyFill(t, mq); got != "Fuzzy from TM50" {
		t.Errorf("tm_order: expected TM50's fuzzy match, got %q", got)
	}
	if len(mq.updateHumanCalls) != 0 {
		t.Errorf("a fuzzy match is never auto-approved; got %d human updates", len(mq.updateHumanCalls))
	}
	hist := findHistoryCall(mq.insertHistoryCalls, db.TranslationHistoryKindTm)
	if hist == nil || hist.TmID == nil || *hist.TmID != 50 {
		t.Errorf("expected the TM history row to name TM 50, got %+v", hist)
	}
}

func TestTMLookupOrder_TMOrder_FallsThroughToNextTM(t *testing.T) {
	mq := newLookupOrderFixture(settings.TMLookupOrderTMOrder)
	delete(mq.fuzzyTMEntries, "50:en:de:Reset your password")
	runLookupOrder(t, mq)

	if got := onlyFill(t, mq); got != "Exact from TM51" {
		t.Errorf("tm_order with nothing in TM50: expected TM51's exact match, got %q", got)
	}
	if len(mq.updateHumanCalls) != 1 {
		t.Errorf("expected TM51's exact match to be auto-approved, got %d human updates", len(mq.updateHumanCalls))
	}
}

func TestTMLookupOrder_TMOrder_ExactBeforeFuzzyWithinOneTM(t *testing.T) {
	mq := newLookupOrderFixture(settings.TMLookupOrderTMOrder)
	mq.tmEntries[tmKey(50, "en", "de", "Reset your password")] = db.TranslationMemory{
		TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Reset your password", TargetText: "Exact from TM50",
	}
	runLookupOrder(t, mq)

	if got := onlyFill(t, mq); got != "Exact from TM50" {
		t.Errorf("tm_order: expected TM50's exact match over its own fuzzy one, got %q", got)
	}
}

// Under tm_order each TM runs its own exact pass, whose count starts at zero.
// The reported progress must still only go up: TM 50 fills segment 100 exactly,
// then TM 51's exact pass fills nothing before its fuzzy pass fills 101.
func TestTMLookupOrder_TMOrder_ProgressNeverGoesBack(t *testing.T) {
	mq := newLookupOrderFixture(settings.TMLookupOrderTMOrder)
	mq.fuzzyTMEntries = map[string][]db.FuzzyLookupTMRow{}
	mq.tmEntries = map[string]db.TranslationMemory{}
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Reset your password"},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "Change your email"},
	}
	mq.tmEntries[tmKey(50, "en", "de", "Reset your password")] = db.TranslationMemory{
		TmID: 50, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Reset your password", TargetText: "Exact from TM50",
	}
	mq.fuzzyTMEntries["51:en:de:Change your email"] = []db.FuzzyLookupTMRow{{
		ID: 2, TmID: 51, SourceLanguage: "en", TargetLanguage: "de",
		SourceText: "Change your email!", TargetText: "Fuzzy from TM51", Score: 0.9,
	}}

	var tmProgress []int
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	if _, err := svc.TranslateFile(context.Background(), int64(1), 1, "en", "de", func(phase string, done, total int) {
		if phase == "tm" {
			tmProgress = append(tmProgress, done)
		}
	}, FullPipeline); err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	if len(mq.updateTMFuzzyCalls) != 2 {
		t.Fatalf("expected both segments filled from TM, got %d fills", len(mq.updateTMFuzzyCalls))
	}
	if len(tmProgress) < 3 {
		t.Fatalf("expected a progress report per pass plus the total, got %v", tmProgress)
	}
	for i := 1; i < len(tmProgress); i++ {
		if tmProgress[i] < tmProgress[i-1] {
			t.Fatalf("TM progress went backwards: %v", tmProgress)
		}
	}
	if last := tmProgress[len(tmProgress)-1]; last != 2 {
		t.Errorf("final TM progress = %d, want 2 (%v)", last, tmProgress)
	}
}
