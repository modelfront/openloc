package ai

import (
	"strings"
	"testing"
)

// A comment-only prompt advises and nothing else: it stores neither a status
// nor a text output, so the model is asked for a comment and must not be asked
// for, or have acted on, a verdict, a score or a postedit.

func TestResultsSchema_CommentAsksForACommentOnly(t *testing.T) {
	props := schemaProps(t, ResultsSchema(ModeComment))
	for _, want := range []string{"id", "comment"} {
		if _, ok := props[want]; !ok {
			t.Errorf("comment-only schema is missing %q", want)
		}
	}
	for _, unwanted := range []string{"approved", "score", "postedit", "postedit_approved", "postedit_score", "target"} {
		if _, ok := props[unwanted]; ok {
			t.Errorf("comment-only schema must not ask for %q", unwanted)
		}
	}
}

func TestPredictSuffix_CommentContractNamesOnlyAComment(t *testing.T) {
	// A model with no structured-output support gets the prose contract.
	got := predictSystemPrompt(PredictConfig{
		Provider: ProviderOpenAI, Model: "gpt-4-turbo",
		PromptText: "Flag issues.", SourceLang: "en", TargetLang: "de",
		Mode: ModeComment,
	})
	if !strings.Contains(got, `"comment"`) {
		t.Errorf("comment-only contract must ask for a comment:\n%s", got)
	}
	for _, unwanted := range []string{`"approved"`, `"score"`, `"postedit"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("comment-only contract must not name %s:\n%s", unwanted, got)
		}
	}
}

func TestPredictConfig_CommentModeIsNotPostEditing(t *testing.T) {
	if (PredictConfig{Mode: ModeComment}).wantsPostedit() {
		t.Error("a comment-only run must not be treated as post-editing")
	}
}

// Whatever else the model volunteers, a comment-only result carries the
// comment and nothing that could change the segment.
func TestParsePredictOutput_CommentIgnoresEverythingButTheComment(t *testing.T) {
	res := parsePredictOutput(`{"comment": "Term mismatch: use Datei.", "approved": true, "score": 40,
		"postedit": "Datei speichern.", "postedit_approved": true, "postedit_score": 90}`, ModeComment)
	if res.Invalid {
		t.Fatalf("comment-only output must parse, got Invalid (raw %q)", res.Raw)
	}
	if res.Comment == nil || *res.Comment != "Term mismatch: use Datei." {
		t.Errorf("comment did not survive: %v", res.Comment)
	}
	if res.Approved || res.Score != nil || res.Postedit != nil || res.PosteditApproved != nil || res.PosteditScore != nil {
		t.Errorf("comment-only result carries more than a comment: %+v", res)
	}
}

// "comment" is the mode's one field, so its absence means the model ignored
// the contract; an explicit null is an answer ("nothing to flag").
func TestParsePredictOutput_CommentRequiresTheCommentKey(t *testing.T) {
	if got := parsePredictOutput(`{"approved": true}`, ModeComment); !got.Invalid {
		t.Error("comment-only output with no comment key must be flagged Invalid")
	}
	got := parsePredictOutput(`{"comment": null}`, ModeComment)
	if got.Invalid {
		t.Error(`"comment": null is a valid comment-only answer`)
	}
	if got.Comment != nil {
		t.Errorf("null comment came back as %q", *got.Comment)
	}
}

// A comment that is not a string breaks the contract in every mode: in
// comment-only it is the one field, and in the judging modes a reply that
// ignored the shape must not have its verdict acted on.
func TestParsePredictOutput_NonStringCommentIsInvalid(t *testing.T) {
	for _, mode := range []string{ModeComment, ModeQE, ModeQEAPE, ModeAPE} {
		for _, raw := range []string{
			`{"comment": 42, "approved": true, "postedit": null}`,
			`{"comment": {}, "approved": true, "postedit": null}`,
			`{"comment": [], "approved": true, "postedit": null}`,
		} {
			if got := parsePredictOutput(raw, mode); !got.Invalid {
				t.Errorf("mode %s: %s must be Invalid", mode, raw)
			}
		}
	}
}
