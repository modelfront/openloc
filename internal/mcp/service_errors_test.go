package mcp

import (
	"strings"
	"testing"

	"openloc/internal/qa"
	"openloc/internal/translation"
)

// A tag the source does not have cannot be ignored, so the message tells the
// assistant to remove it rather than to ignore a finding that is not stored.
func TestServiceErrQABlock_TagNotInSourceIsNotIgnorable(t *testing.T) {
	tagOnly := serviceErr(&translation.QABlockedError{Issues: []qa.Issue{{
		CheckType: translation.SourceTagsCheckType, Severity: "error", Message: "Tag <b> is not in the source",
	}}}, "confirm").Error()
	if strings.Contains(tagOnly, "openloc_ignore_qa_finding") {
		t.Errorf("tag refusal suggests ignoring: %q", tagOnly)
	}
	if !strings.Contains(tagOnly, "Tag <b> is not in the source") || !strings.Contains(tagOnly, "Remove the tag") {
		t.Errorf("tag refusal message = %q, want the tag and an instruction to remove it", tagOnly)
	}

	profile := serviceErr(&translation.QABlockedError{Issues: []qa.Issue{{
		CheckType: "number_consistency", Severity: "error", Message: "Missing number(s) in target: 2026",
	}}}, "confirm").Error()
	if !strings.Contains(profile, "openloc_ignore_qa_finding") {
		t.Errorf("profile QA refusal lost the ignore hint: %q", profile)
	}
}
