package pipeline

import (
	"context"
	"strings"
	"testing"

	"openloc/internal/ai"
	db "openloc/internal/database/sqlcgen"
)

// The unit tests in internal/aiprompt cover the matching rule. What matters
// here is that the pipeline actually loads the additions and hands the combined
// text to the provider — a rule that matches perfectly but never reaches the
// request is worth nothing.

// fixtureSystemPromptID is the prompt the LLM-step fixture attaches to its engine row.
const fixtureSystemPromptID int64 = 903

// addLangAddition adds a TARGET-language addition — "any job into these".
func addLangAddition(mq *mockQuerier, position int32, text string, targetLangs ...string) {
	mq.aiPromptLangAdditions[fixtureSystemPromptID] = append(
		mq.aiPromptLangAdditions[fixtureSystemPromptID],
		db.AiPromptLanguageAddition{
			ID:              int64(position) + 1,
			PromptID:        fixtureSystemPromptID,
			Position:        position,
			AdditionText:    text,
			TargetLanguages: targetLangs,
		},
	)
}

// runLLMStepWithAdditions runs a one-segment QE step on an en→targetLang job
// and returns the prompt text the predictor was handed.
func runLLMStepWithAdditions(t *testing.T, targetLang string, setup func(*mockQuerier)) string {
	t.Helper()
	// Keyed by the segment number the wire carries, not the row id.
	fp := &fakePredictor{results: map[string]ai.PredictResult{"1": {Approved: true}}}

	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{targetLang})
	mq.setAIProviderConn(1, "openai", "sk-test")
	mq.addLLMAIStepJob(5, 1, 1, targetLang, "qe", "LLM QE", "openai", "reject")
	setup(mq)

	mtText := "Hallo"
	mq.preTranslated[1] = []db.ListPreTranslatedByFileRow{
		{ID: 100, SegmentID: 1, Position: 0, TargetLanguage: targetLang, SourceText: "Hello", ActiveText: &mtText},
	}

	if _, err := newLLMAIStepService(mq, fp).RunAIStep(context.Background(), 1, 5, nil); err != nil {
		t.Fatalf("RunAIStep: %v", err)
	}
	if len(fp.gotCfgs) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(fp.gotCfgs))
	}
	return fp.gotCfgs[0].PromptText
}

func TestRunAIStep_AppendsMatchingLanguageAdditions(t *testing.T) {
	t.Run("an addition for the job's target language is sent", func(t *testing.T) {
		got := runLLMStepWithAdditions(t, "de", func(mq *mockQuerier) {
			addLangAddition(mq, 0, "Address the reader formally.", "de")
		})
		if !strings.Contains(got, "Evaluate carefully.") {
			t.Errorf("the prompt's own text must survive: %q", got)
		}
		if !strings.Contains(got, "Address the reader formally.") {
			t.Errorf("the matching addition must reach the provider: %q", got)
		}
	})

	t.Run("an addition for another language is not sent", func(t *testing.T) {
		got := runLLMStepWithAdditions(t, "fr", func(mq *mockQuerier) {
			addLangAddition(mq, 0, "Address the reader formally.", "de")
		})
		if strings.Contains(got, "Address the reader formally.") {
			t.Errorf("a German addition must not ride on a French job: %q", got)
		}
	})

	t.Run("a sublanguage job still matches", func(t *testing.T) {
		got := runLLMStepWithAdditions(t, "de-at", func(mq *mockQuerier) {
			addLangAddition(mq, 0, "Address the reader formally.", "de")
		})
		if !strings.Contains(got, "Address the reader formally.") {
			t.Errorf("de-AT must inherit the de addition: %q", got)
		}
	})

	t.Run("a prompt with no additions is unchanged", func(t *testing.T) {
		got := runLLMStepWithAdditions(t, "de", func(*mockQuerier) {})
		if got != "Evaluate carefully." {
			t.Errorf("got %q, want the base prompt untouched", got)
		}
	})
}
