package pipeline

import (
	"context"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
)

// The step's AI router decides which engine reviews a job by the job's TARGET
// language. These two tests cover the WIRING at
// aistep.go's airouter.ResolveEngineID call — the resolution rule itself is
// covered in internal/airouter.

// A router whose rules cover some other language and which has no default
// engine leaves nothing to review this job: a benign skip, not an error, and
// no segment is touched. The re-run endpoint turns the reason into a 400 that
// names the fix (internal/job: TestIntegrationAIStep_NoEngineForTargetLanguage_Returns400).
func TestRunAIStep_RouterHasNoEngineForTargetLanguage_Skips(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	// The extension IS installed, so a skip here can only be the language one.
	mq.addAIStepJob(5, 1, 1, "de", "APE review")
	// The only rule is on the SOURCE language, and there is no default to fall
	// back on — so this stays red if the resolver is handed the source language.
	mq.setAIRouterRules(900, nil, routerRule{targetLang: "en", engineID: 901})

	mt := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "de", SourceText: "Hello", ActiveText: &mt},
	}

	svc, _ := newExtStepService(mq, &fakeExtPredictor{})
	res, err := svc.RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("a router with no engine for the language is a benign skip, got error: %v", err)
	}
	if res.SkipReason != PhaseSkipNoEngineForLanguage {
		t.Errorf("SkipReason = %q, want %q", res.SkipReason, PhaseSkipNoEngineForLanguage)
	}
	if len(mq.updatePredictCalls) != 0 {
		t.Errorf("nothing may be reviewed when no engine routes the language, got %d predict updates", len(mq.updatePredictCalls))
	}
}

// A rule for the job's target language beats the router's default engine. The
// languages are deliberately asymmetric (en → ja, with the decoy default
// reachable only via a rule on the SOURCE language): resolving on the source
// language instead would pick the decoy, and the predict config names which
// engine ran.
func TestRunAIStep_RouterRuleForTargetLanguageBeatsDefault(t *testing.T) {
	fp := &fakePredictor{results: map[string]ai.PredictResult{"1": {Approved: true}}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"ja"})
	mq.setAIProviderConn(1, "openai", "sk-test")
	// Engine 901: the rule's engine, prompt "Evaluate carefully." / model "test-model".
	mq.addLLMAIStepJob(5, 1, 1, "ja", "qe_ape", "LLM APE", "openai", "reject")
	// Engine 800: the decoy the router defaults to — same language pair, so a
	// wrong pick still runs and is caught by the config, not by a skip.
	mq.addLLMAIEngine(800, 1, "en", "ja", "openai", "Decoy prompt.", "decoy-model")
	decoyID := int64(800)
	mq.setAIRouterRules(900, &decoyID, routerRule{targetLang: "ja", engineID: 901})

	mt := "こんにちは"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: "ja", SourceText: "Hello", ActiveText: &mt},
	}

	res, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if res.SkipReason != "" {
		t.Fatalf("the rule routes this job, so nothing may be skipped; got %q", res.SkipReason)
	}
	if len(fp.gotCfgs) != 1 {
		t.Fatalf("expected 1 predict batch, got %d", len(fp.gotCfgs))
	}
	cfg := fp.gotCfgs[0]
	if cfg.Model != "test-model" || cfg.PromptText != "Evaluate carefully." {
		t.Errorf("the rule's engine must review the job, got model %q / prompt %q (the decoy default is %q / %q)",
			cfg.Model, cfg.PromptText, "decoy-model", "Decoy prompt.")
	}
	if cfg.SourceLang != "en" || cfg.TargetLang != "ja" {
		t.Errorf("language pair = %s→%s, want en→ja", cfg.SourceLang, cfg.TargetLang)
	}
	if len(mq.updatePredictCalls) != 1 {
		t.Fatalf("expected 1 predict update, got %d", len(mq.updatePredictCalls))
	}
	if got := mq.updatePredictCalls[0].Status; got != db.TranslationStatusAutoApproved {
		t.Errorf("status = %s, want auto_approved", got)
	}
}
