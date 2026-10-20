package ai

import (
	"strings"
	"testing"
)

// An APE prompt stores out_status = false — that flag is what tells it apart
// from QE+APE (aiprompt.ModeForFlags). A mode that declares it emits no status
// must not be asked for one, and must not have one acted on: the whole APE
// contract is "post-edit every translation", with the human reviewing the
// result.

// schemaProps digs out the per-result property map — the part that says which
// fields the model must return.
func schemaProps(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	results := schema["properties"].(map[string]any)["results"].(map[string]any)
	return results["items"].(map[string]any)["properties"].(map[string]any)
}

func TestResultsSchema_APEAsksForNoVerdict(t *testing.T) {
	props := schemaProps(t, ResultsSchema(ModeAPE))

	for _, want := range []string{"id", "postedit", "comment"} {
		if _, ok := props[want]; !ok {
			t.Errorf("APE schema is missing %q", want)
		}
	}
	// The judging half belongs to QE and QE+APE. Asking APE for it is what
	// lets an APE engine auto-approve, and a score is that same judgement
	// wearing a number.
	for _, unwanted := range []string{"approved", "postedit_approved", "postedit_score", "score"} {
		if _, ok := props[unwanted]; ok {
			t.Errorf("APE schema must not ask for %q", unwanted)
		}
	}
}

// QE+APE is unchanged — it is the mode that judges.
func TestResultsSchema_QEAPEStillAsksForAVerdict(t *testing.T) {
	props := schemaProps(t, ResultsSchema(ModeQEAPE))
	for _, want := range []string{"postedit", "approved", "score", "postedit_approved", "postedit_score", "comment"} {
		if _, ok := props[want]; !ok {
			t.Errorf("QE+APE schema is missing %q", want)
		}
	}
}

func TestPredictSuffix_APEContractNamesNoApproval(t *testing.T) {
	// Pinned to a model with no structured-output support: on a schema-capable
	// one the prose half is deliberately dropped.
	got := predictSystemPrompt(PredictConfig{
		Provider: ProviderOpenAI, Model: "gpt-4-turbo",
		PromptText: "Fix it.", SourceLang: "en", TargetLang: "de",
		Mode: ModeAPE,
	})
	if !strings.Contains(got, `"postedit"`) {
		t.Errorf("APE contract must ask for a postedit:\n%s", got)
	}
	for _, unwanted := range []string{`"approved"`, `"postedit_approved"`, `"postedit_score"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("APE contract must not name %s:\n%s", unwanted, got)
		}
	}
}

// The APE contract asks for no verdict, so one a model volunteers anyway is
// ignored: the postedit and score survive, and nothing comes back approved.
func TestParsePredictOutput_APEIgnoresAVolunteeredVerdict(t *testing.T) {
	res := parsePredictOutput(`{"postedit": "Datei speichern.", "score": 88, "comment": null,
		"approved": true, "postedit_approved": true, "postedit_score": 90}`, ModeAPE)
	if res.Invalid {
		t.Fatalf("APE output must parse, got Invalid (raw %q)", res.Raw)
	}
	if res.Postedit == nil || *res.Postedit != "Datei speichern." {
		t.Errorf("postedit did not survive: %v", res.Postedit)
	}
	if res.Score == nil || *res.Score != 88 {
		t.Errorf("score did not survive: %v", res.Score)
	}
	if res.Approved {
		t.Error("an APE result must never come back approved")
	}
	if res.PosteditApproved != nil {
		t.Errorf("postedit_approved = %v, want nil on an APE result", *res.PosteditApproved)
	}
	if res.PosteditScore != nil {
		t.Errorf("postedit_score = %v, want nil on an APE result", *res.PosteditScore)
	}
}

// "postedit" is APE's required field, so its absence is the signal that the
// model ignored the contract — the same job "approved" does for QE.
func TestParsePredictOutput_APERejectsOutputWithNoPostedit(t *testing.T) {
	if got := parsePredictOutput(`{"score": 88}`, ModeAPE); !got.Invalid {
		t.Error("APE output with no postedit key must be flagged Invalid")
	}
	// Explicit null is an answer: "nothing to fix".
	if got := parsePredictOutput(`{"postedit": null, "score": 88}`, ModeAPE); got.Invalid {
		t.Error(`"postedit": null is a valid APE answer`)
	}
}

func TestParsePredictOutput_QEStillRequiresAVerdict(t *testing.T) {
	if got := parsePredictOutput(`{"score": 88}`, ModeQE); !got.Invalid {
		t.Error("QE output with no approved key must be flagged Invalid")
	}
}
