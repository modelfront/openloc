package qa

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func stored(checkType, message string, ignored bool) db.ListQAResultsForTranslationRow {
	return db.ListQAResultsForTranslationRow{CheckType: checkType, Message: message, Ignored: ignored}
}

func TestMarkIgnored_MatchesOnCheckTypeAndMessage(t *testing.T) {
	issues := []Issue{
		{CheckType: "number_consistency", Severity: "error", Message: "Missing number(s) in target: 42"},
		{CheckType: "punctuation", Severity: "warning", Message: "Punctuation mismatch — ends differ"},
	}
	out := markIgnored(issues, []db.ListQAResultsForTranslationRow{
		stored("number_consistency", "Missing number(s) in target: 42", true),
		stored("punctuation", "Punctuation mismatch — ends differ", false),
	})
	if !out[0].Ignored {
		t.Error("number_consistency: stored ignored row with same message should mark the issue ignored")
	}
	if out[1].Ignored {
		t.Error("punctuation: stored row is NOT ignored, issue must not be marked")
	}
}

func TestMarkIgnored_ChangedMessageIsANewFinding(t *testing.T) {
	issues := []Issue{
		{CheckType: "tag_missing", Severity: "error", Message: "Source tag(s) missing from target: </1>"},
	}
	out := markIgnored(issues, []db.ListQAResultsForTranslationRow{
		stored("tag_missing", "Source tag(s) missing from target: <2>", true),
	})
	if out[0].Ignored {
		t.Error("a different missing tag is a different finding; the old ignore must not carry over")
	}
}

func TestMarkIgnored_NoStoredRowsLeavesIssuesUntouched(t *testing.T) {
	issues := []Issue{{CheckType: "empty_target", Severity: "error", Message: "Target segment is empty"}}
	out := markIgnored(issues, nil)
	if out[0].Ignored {
		t.Error("nothing stored → nothing ignored")
	}
}

func TestHasErrors_SkipsIgnoredErrors(t *testing.T) {
	r := CheckResult{Issues: []Issue{
		{CheckType: "tag_missing", Severity: "error", Ignored: true},
		{CheckType: "punctuation", Severity: "warning"},
	}}
	if r.HasErrors() {
		t.Error("the only error is ignored; HasErrors must be false so confirm is not blocked")
	}
	r.Issues[0].Ignored = false
	if !r.HasErrors() {
		t.Error("un-ignored error must block again")
	}
}
