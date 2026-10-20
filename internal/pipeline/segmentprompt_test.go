package pipeline

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/segmentprompt"
	"openloc/internal/translationmemory"
)

// Per-segment body generation, exercised through RunAIStep (the QE+APE path)
// because that is where a real body reaches a provider. The asserted contract
// is threefold:
//  1. the model's per-segment body IS the object the prompt's context describes;
//  2. resolution is FIELD-DRIVEN — context the prompt did not ask for costs no
//     query, and context a segment has no value for leaves no empty key;
//  3. the context that IS asked for is fetched once per batch, not per segment.

// llmStepFixture seeds a 2-segment QE+APE job whose system prompt carries the
// given optional context.
func llmStepFixture(t *testing.T, context ...string) *mockQuerier {
	t.Helper()
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, "de", "qe_ape", "LLM APE", "openai", "reject")
	mq.setSegmentContext(context...)

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello A", ActiveText: &mt},
		{ID: 101, SegmentID: 2, Position: 1, TargetLanguage: "de", SourceText: "Hello B", ActiveText: &mt},
	}
	return mq
}

// runStep runs the fixture and returns the per-segment bodies the predictor saw.
func runStep(t *testing.T, mq *mockQuerier) []json.RawMessage {
	t.Helper()
	fp := &fakePredictor{results: map[string]ai.PredictResult{
		// Keyed by the SEGMENT NUMBER the wire carries, not the row id.
		"1": {Approved: true}, "2": {Approved: true},
	}}
	if _, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if len(fp.gotInputs) != 1 || len(fp.gotInputs[0]) != 2 {
		t.Fatalf("expected one batch of 2 inputs, got %+v", fp.gotInputs)
	}
	return []json.RawMessage{fp.gotInputs[0][0].Content, fp.gotInputs[0][1].Content}
}

func decodeBody(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not a JSON object: %s (%v)", raw, err)
	}
	return body
}

// onlyTMMatch returns the single entry of a body's tm_matches list.
func onlyTMMatch(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	list, ok := decodeBody(t, raw)["tm_matches"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("body should carry a one-item tm_matches list: %s", raw)
	}
	match, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("tm_matches[0] is not an object: %s", raw)
	}
	return match
}

// The source text and the translation always travel on a reviewing mode — they
// are what the model is judging, so they are implied rather than chosen.
func TestRunAIStep_BodyAlwaysCarriesSourceAndTranslation(t *testing.T) {
	bodies := runStep(t, llmStepFixture(t))

	got := decodeBody(t, bodies[0])
	want := map[string]any{"source": "Hello A", "translation": "Hallo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

// The tm_match setting triggers a TM lookup per segment — and only then.
func TestRunAIStep_ResolvesTMMatch(t *testing.T) {
	mq := llmStepFixture(t, segmentprompt.KeyTMMatch)
	mq.settingsTMs[langKey(10, "en", "de")] = []db.LookupSettingsTMsRow{{TmID: 7, Mode: "read_write", Priority: 0}}
	mq.fuzzyTMEntries["7:en:de:Hello A"] = []db.FuzzyLookupTMRow{
		{ID: 1, TmID: 7, SourceLanguage: "en", TargetLanguage: "de", SourceText: "Hello A", TargetText: "Hallo A (aus TM)", Score: 0.95},
	}

	bodies := runStep(t, mq)

	// The pair: the match's source is the half the model
	// cannot otherwise see, and where a fuzzy-match defect shows up.
	match := onlyTMMatch(t, bodies[0])
	if match["source"] != "Hello A" || match["target"] != "Hallo A (aus TM)" {
		t.Errorf("tm_matches[0] = %v, want both halves of the matched entry", match)
	}
	if match["score"] != float64(100) {
		t.Errorf("tm_matches[0].score = %v, want 100 (identical source text)", match["score"])
	}

	// Segment 101 has no TM hit — the key drops rather than travelling empty.
	if _, present := decodeBody(t, bodies[1])["tm_matches"]; present {
		t.Errorf("segment 101 has no TM match, so the key must drop: %s", bodies[1])
	}
}

// When a TM match exists, BOTH halves are always sent — even when the matched
// target is identical to the translation.
//
// Omitting the duplicate was tried and reverted: from the model's side an absent
// target is ambiguous, since "identical, so removed" and "no target" look the
// same. Sending both lets it VERIFY the relationship — target == translation
// means the segment inherited this entry, which is precisely when the source
// comparison matters most.
func TestRunAIStep_TMMatchAlwaysSendsBothHalves(t *testing.T) {
	// The fixture's translation is "Hallo".
	for _, tc := range []struct{ name, tmTarget string }{
		{"target identical to the translation", "Hallo"},
		{"target different from the translation", "Hallo A (aus TM)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mq := llmStepFixture(t, segmentprompt.KeyTMMatch)
			mq.settingsTMs[langKey(10, "en", "de")] = []db.LookupSettingsTMsRow{{TmID: 7, Mode: "read_write", Priority: 0}}
			mq.fuzzyTMEntries["7:en:de:Hello A"] = []db.FuzzyLookupTMRow{
				{ID: 1, TmID: 7, SourceLanguage: "en", TargetLanguage: "de",
					SourceText: "Hello A", TargetText: tc.tmTarget, Score: 0.95},
			}

			bodies := runStep(t, mq)

			match := onlyTMMatch(t, bodies[0])
			if match["source"] != "Hello A" {
				t.Errorf("tm_matches[0].source = %v, want the matched source", match["source"])
			}
			if match["target"] != tc.tmTarget {
				t.Errorf("tm_matches[0].target = %v, want %q — both halves always travel together",
					match["target"], tc.tmTarget)
			}
		})
	}
}

// The score is the one after penalties: a TM the profile distrusts is docked,
// and only the score tells the model so — the texts look the same.
func TestRunAIStep_TMMatchScoreIsAfterPenalties(t *testing.T) {
	mq := llmStepFixture(t, segmentprompt.KeyTMMatch)
	mq.settingsTMs[langKey(10, "en", "de")] = []db.LookupSettingsTMsRow{{TmID: 7, Mode: "read_write", Priority: 0, PenaltyPct: 10}}
	mq.fuzzyTMEntries["7:en:de:Hello A"] = []db.FuzzyLookupTMRow{
		{ID: 1, TmID: 7, SourceLanguage: "en", TargetLanguage: "de", SourceText: "Hello A", TargetText: "Hallo A", Score: 0.95},
	}

	bodies := runStep(t, mq)

	if got := onlyTMMatch(t, bodies[0])["score"]; got != float64(90) {
		t.Errorf("tm_matches[0].score = %v, want 90 (an identical source less the 10%% TM penalty)", got)
	}
}

// A tagged segment's TM lookup matches on its plain source, as the TM phase
// does. The chip labels the model reads are not text a TM entry contains, so
// matching on them would lower the score or miss the entry.
func TestRunAIStep_TMMatchLooksUpPlainSourceOfTaggedSegment(t *testing.T) {
	mq := llmStepFixture(t, segmentprompt.KeyTMMatch)
	rich := `<g id="1" ctype="bold">Save</g> now`
	mq.preTranslated[1][0].SourceText = "Save now"
	mq.preTranslated[1][0].SourceRichText = &rich
	mq.settingsTMs[langKey(10, "en", "de")] = []db.LookupSettingsTMsRow{{TmID: 7, Mode: "read_write", Priority: 0}}
	mq.fuzzyTMEntries["7:en:de:Save now"] = []db.FuzzyLookupTMRow{
		{ID: 1, TmID: 7, SourceLanguage: "en", TargetLanguage: "de", SourceText: "Save now", TargetText: "Jetzt speichern", Score: 1},
	}

	bodies := runStep(t, mq)

	match := onlyTMMatch(t, bodies[0])
	if match["source"] != "Save now" || match["score"] != float64(100) {
		t.Errorf("tm_matches[0] = %v, want the entry for the plain source with score 100", match)
	}
}

// Glossary terms matching a segment's source text reach the model, so it can
// apply the tenant's approved wording instead of guessing at it.
func TestRunAIStep_ResolvesTerms(t *testing.T) {
	mq := llmStepFixture(t, segmentprompt.KeyTerms)
	// "Hello" appears in both segments' source text; "world" in neither.
	mq.settingsTerms[langKey(10, "en", "de")] = []db.LookupSettingsTermsByTokensRow{
		{SourceTerm: "Hello", TargetTerm: "Hallo", Status: "approved"},
		{SourceTerm: "world", TargetTerm: "Welt", Status: "forbidden"},
	}

	bodies := runStep(t, mq)

	terms, ok := decodeBody(t, bodies[0])["terms"].([]any)
	if !ok {
		t.Fatalf("segment 100 should carry a terms array: %s", bodies[0])
	}
	if len(terms) != 1 {
		t.Fatalf("want only the term that appears in this segment, got %v", terms)
	}
	term := terms[0].(map[string]any)
	if term["source"] != "Hello" || term["target"] != "Hallo" {
		t.Errorf("term = %v, want the Hello/Hallo pair", term)
	}
	// Status travels because a forbidden term is the one the model most needs
	// to know about.
	if term["status"] != "approved" {
		t.Errorf("term status = %v, want approved", term["status"])
	}
}

// One query for the whole batch, not one per segment — the per-segment lookup
// the CAT sidebar uses would be llmBatchSize queries per request.
func TestRunAIStep_BatchesTermQueries(t *testing.T) {
	mq := llmStepFixture(t, segmentprompt.KeyTerms)
	mq.settingsTerms[langKey(10, "en", "de")] = []db.LookupSettingsTermsByTokensRow{
		{SourceTerm: "Hello", TargetTerm: "Hallo", Status: "approved"},
	}

	runStep(t, mq)

	if mq.termLookups != 1 {
		t.Errorf("want 1 batched term query for the batch, got %d", mq.termLookups)
	}
}

// …and a prompt that does not ask for terms runs no term query at all.
func TestRunAIStep_NoTermQueryWhenTermsAreOff(t *testing.T) {
	mq := llmStepFixture(t)
	mq.settingsTerms[langKey(10, "en", "de")] = []db.LookupSettingsTermsByTokensRow{
		{SourceTerm: "Hello", TargetTerm: "Hallo", Status: "approved"},
	}

	bodies := runStep(t, mq)

	if mq.termLookups != 0 {
		t.Errorf("terms is off — expected 0 term lookups, got %d", mq.termLookups)
	}
	if _, present := decodeBody(t, bodies[0])["terms"]; present {
		t.Errorf("terms is off, so the key must not appear: %s", bodies[0])
	}
}

// The <tm_match> placeholder reads the threshold as the TM phase does: 0 keeps
// every candidate, a negative value or an unreadable profile falls back to
// the default.
func TestResolveTMMatchConfig_Threshold(t *testing.T) {
	mq := newMockQuerier()
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil)
	for _, c := range []struct {
		name      string
		threshold float32
		want      float32
	}{
		{"zero is honoured", 0, 0},
		{"a set threshold", 0.6, 0.6},
		{"negative falls back", -1, translationmemory.DefaultFuzzyThreshold},
	} {
		t.Run(c.name, func(t *testing.T) {
			mq.settingsProfiles[7] = db.SettingsProfile{ID: 7, FuzzyThreshold: c.threshold}
			if got := svc.resolveTMMatchConfig(context.Background(), 7, "en", "es", nil).fuzzyThreshold; got != c.want {
				t.Errorf("threshold %v read as %v, want %v", c.threshold, got, c.want)
			}
		})
	}
	if got := svc.resolveTMMatchConfig(context.Background(), 404, "en", "es", nil).fuzzyThreshold; got != translationmemory.DefaultFuzzyThreshold {
		t.Errorf("unreadable profile: threshold %v, want the default", got)
	}
}
