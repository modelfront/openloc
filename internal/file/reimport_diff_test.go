package file

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
)

// rowsFromFixtures builds DB rows for the diff function. Keeps tests legible
// without needing a real database — computeReimportDiff is pure.
func rowsFromFixtures(specs ...rowSpec) []db.ListTranslationsByFileForReimportRow {
	rows := make([]db.ListTranslationsByFileForReimportRow, 0, len(specs))
	for i, s := range specs {
		key := s.key
		var keyPtr *string
		if key != "" {
			keyPtr = &key
		}
		var activeText *string
		if s.activeText != "" {
			t := s.activeText
			activeText = &t
		}
		var activeRich *string
		if s.activeRich != "" {
			r := s.activeRich
			activeRich = &r
		}
		rows = append(rows, db.ListTranslationsByFileForReimportRow{
			TranslationID:  int64(100 + i),
			SegmentID:      int64(200 + i),
			SegmentKey:     keyPtr,
			Locked:         s.locked,
			Status:         s.status,
			ActiveText:     activeText,
			ActiveRichText: activeRich,
		})
	}
	return rows
}

type rowSpec struct {
	key        string
	status     db.TranslationStatus
	locked     bool
	activeText string
	activeRich string
}

func TestComputeReimportDiff_HappyPath_TwoUpdates(t *testing.T) {
	rows := rowsFromFixtures(
		rowSpec{key: "u1", status: db.TranslationStatusNeedsReview},
		rowSpec{key: "u2", status: db.TranslationStatusNeedsReview, activeText: "Old MT"},
	)
	incoming := []parser.Segment{
		{Key: "u1", Target: "Hello"},
		{Key: "u2", Target: "Bonjour"},
	}
	result, plan := computeReimportDiff(rows, incoming, false)

	if result.Updated != 2 {
		t.Errorf("Updated: want 2, got %d", result.Updated)
	}
	if result.SkippedLocked+result.SkippedApproved+result.UntouchedNoChange+result.ExtraInUpload+result.MissingFromUpload != 0 {
		t.Errorf("expected zero non-update counts, got %+v", result)
	}
	if result.ForceRequired {
		t.Error("ForceRequired should be false on a clean update set")
	}
	if len(plan) != 2 {
		t.Fatalf("plan len: want 2, got %d", len(plan))
	}
}

func TestComputeReimportDiff_NoChange_DetectedFromActiveText(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusApproved, activeText: "Hello"})
	incoming := []parser.Segment{{Key: "u1", Target: "Hello"}}

	result, plan := computeReimportDiff(rows, incoming, false)

	if result.UntouchedNoChange != 1 {
		t.Errorf("UntouchedNoChange: want 1, got %d", result.UntouchedNoChange)
	}
	if result.Updated != 0 || result.SkippedApproved != 0 {
		t.Errorf("no-change must short-circuit before status check, got %+v", result)
	}
	if len(plan) != 0 {
		t.Errorf("plan must be empty for no-change, got %d entries", len(plan))
	}
}

func TestComputeReimportDiff_EmptyIncomingTargetIsNoop(t *testing.T) {
	// R4: an empty <target/> in the upload is NOT a wipe.
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusApproved, activeText: "Existing translation"})
	incoming := []parser.Segment{{Key: "u1", Target: ""}}

	result, plan := computeReimportDiff(rows, incoming, true) // even with force=true

	if result.UntouchedNoChange != 1 {
		t.Errorf("empty target must be untouched, got %+v", result)
	}
	if len(plan) != 0 {
		t.Errorf("plan must be empty when incoming target is empty, got %d entries", len(plan))
	}
}

func TestComputeReimportDiff_LockedAlwaysSkipped_EvenWithForce(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusNeedsReview, locked: true})
	incoming := []parser.Segment{{Key: "u1", Target: "New"}}

	result, plan := computeReimportDiff(rows, incoming, true)

	if result.SkippedLocked != 1 {
		t.Errorf("SkippedLocked: want 1, got %d", result.SkippedLocked)
	}
	if result.Updated != 0 {
		t.Errorf("Updated: want 0 (locked overrides force), got %d", result.Updated)
	}
	if len(plan) != 0 {
		t.Errorf("plan must be empty for locked rows, got %d", len(plan))
	}
}

func TestComputeReimportDiff_ApprovedSetsForceRequired(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusApproved, activeText: "Old"})
	incoming := []parser.Segment{{Key: "u1", Target: "New"}}

	result, plan := computeReimportDiff(rows, incoming, false)

	if result.SkippedApproved != 1 {
		t.Errorf("SkippedApproved: want 1, got %d", result.SkippedApproved)
	}
	if !result.ForceRequired {
		t.Error("ForceRequired must be true when approved segments are skipped")
	}
	if result.Updated != 0 {
		t.Errorf("Updated: want 0, got %d", result.Updated)
	}
	if len(plan) != 0 {
		t.Errorf("plan must be empty when approved without force, got %d", len(plan))
	}
}

func TestComputeReimportDiff_ApprovedUpdatedWithForce(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusApproved, activeText: "Old"})
	incoming := []parser.Segment{{Key: "u1", Target: "New"}}

	result, plan := computeReimportDiff(rows, incoming, true)

	if result.Updated != 1 {
		t.Errorf("Updated: want 1, got %d", result.Updated)
	}
	if result.SkippedApproved != 0 {
		t.Errorf("SkippedApproved: want 0 with force, got %d", result.SkippedApproved)
	}
	if result.ForceRequired {
		t.Error("ForceRequired must be false after force overrides")
	}
	if len(plan) != 1 {
		t.Errorf("plan len: want 1, got %d", len(plan))
	}
}

func TestComputeReimportDiff_ExtraInUpload_Counted(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "u1", status: db.TranslationStatusNeedsReview})
	incoming := []parser.Segment{
		{Key: "u1", Target: "Hello"},
		{Key: "u2", Target: "Goodbye"},
		{Key: "u3", Target: "Salut"},
	}
	result, _ := computeReimportDiff(rows, incoming, false)

	if result.ExtraInUpload != 2 {
		t.Errorf("ExtraInUpload: want 2, got %d", result.ExtraInUpload)
	}
	if result.Updated != 1 {
		t.Errorf("Updated: want 1, got %d", result.Updated)
	}
	if len(result.ExtraInUploadKeys) != 2 {
		t.Errorf("ExtraInUploadKeys: want 2, got %v", result.ExtraInUploadKeys)
	}
}

func TestComputeReimportDiff_MissingFromUpload_Counted(t *testing.T) {
	rows := rowsFromFixtures(
		rowSpec{key: "u1", status: db.TranslationStatusNeedsReview},
		rowSpec{key: "u2", status: db.TranslationStatusNeedsReview},
		rowSpec{key: "u3", status: db.TranslationStatusNeedsReview},
	)
	incoming := []parser.Segment{{Key: "u1", Target: "Hello"}}

	result, _ := computeReimportDiff(rows, incoming, false)

	if result.MissingFromUpload != 2 {
		t.Errorf("MissingFromUpload: want 2, got %d", result.MissingFromUpload)
	}
	if result.Updated != 1 {
		t.Errorf("Updated: want 1, got %d", result.Updated)
	}
}

func TestComputeReimportDiff_RichTextChange_DetectedEvenIfPlainIdentical(t *testing.T) {
	// If the user re-arranged tags but kept the plain text, that IS a change.
	// Otherwise rich-text-only edits would be silently dropped.
	rows := rowsFromFixtures(rowSpec{
		key:        "u1",
		status:     db.TranslationStatusNeedsReview,
		activeText: "Hello world",
		activeRich: `Hello <ph id="1"/>world`,
	})
	incoming := []parser.Segment{{
		Key:            "u1",
		Target:         "Hello world",
		TargetRichText: `Hello world<ph id="1"/>`,
	}}

	result, plan := computeReimportDiff(rows, incoming, false)

	if result.Updated != 1 {
		t.Errorf("Updated: want 1 (rich-text-only change), got %d, result=%+v", result.Updated, result)
	}
	if len(plan) != 1 {
		t.Errorf("plan len: want 1, got %d", len(plan))
	}
	if plan[0].newRichText == nil || *plan[0].newRichText != `Hello world<ph id="1"/>` {
		t.Errorf("plan rich text: got %v, want incoming", plan[0].newRichText)
	}
}

func TestComputeReimportDiff_RichTextSame_NoChange(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{
		key:        "u1",
		status:     db.TranslationStatusApproved,
		activeText: "Hello world",
		activeRich: `Hello <ph id="1"/>world`,
	})
	incoming := []parser.Segment{{
		Key:            "u1",
		Target:         "Hello world",
		TargetRichText: `Hello <ph id="1"/>world`,
	}}

	result, _ := computeReimportDiff(rows, incoming, false)

	if result.UntouchedNoChange != 1 {
		t.Errorf("rich-text-identical must be untouched, got %+v", result)
	}
}

// An external tool that renumbers trans-unit ids breaks the match: the row is
// missing and the incoming segment is extra, even with the same source text.
func TestComputeReimportDiff_KeyMismatch_MissingAndExtra(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{key: "old-id", status: db.TranslationStatusNeedsReview})
	incoming := []parser.Segment{{Key: "new-id", Text: "Hello", Target: "Bonjour"}}

	result, plan := computeReimportDiff(rows, incoming, false)

	if result.Updated != 0 {
		t.Errorf("Updated: want 0 on a key mismatch, got %d", result.Updated)
	}
	if result.MissingFromUpload != 1 || result.ExtraInUpload != 1 {
		t.Errorf("want missing=1 extra=1, got %+v", result)
	}
	if len(plan) != 0 {
		t.Errorf("plan should be empty, got %d", len(plan))
	}
}

// Rows imported earlier hold raw markup inside <ph>/<bpt>; the parser now
// returns it escaped. The same tags must not count as a change.
func TestComputeReimportDiff_RichTextEncodingOnly_NoChange(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{
		key:        "u1",
		status:     db.TranslationStatusApproved,
		activeText: "Tom & Jerry bold",
		activeRich: `Tom & Jerry <ph id="1"><br/></ph><bpt id="2"><b></bpt>bold<ept id="2"></b></ept>`,
	})
	incoming := []parser.Segment{{
		Key:            "u1",
		Target:         "Tom & Jerry bold",
		TargetRichText: `Tom & Jerry <ph id="1">&lt;br/&gt;</ph><bpt id="2">&lt;b&gt;</bpt>bold<ept id="2">&lt;/b&gt;</ept>`,
	}}

	result, plan := computeReimportDiff(rows, incoming, false)

	if result.UntouchedNoChange != 1 || result.Updated != 0 || result.SkippedApproved != 0 || result.ForceRequired {
		t.Errorf("want unchanged, got %+v", result)
	}
	if len(plan) != 0 {
		t.Errorf("plan: want empty (stored row kept as is), got %d", len(plan))
	}
}

func TestComputeReimportDiff_RichTextOpaqueContentChange_Detected(t *testing.T) {
	rows := rowsFromFixtures(rowSpec{
		key:        "u1",
		status:     db.TranslationStatusNeedsReview,
		activeText: "Hi",
		activeRich: `Hi<ph id="1"><br/></ph>`,
	})
	incoming := []parser.Segment{{
		Key:            "u1",
		Target:         "Hi",
		TargetRichText: `Hi<ph id="1">&lt;hr/&gt;</ph>`,
	}}

	result, _ := computeReimportDiff(rows, incoming, false)

	if result.Updated != 1 {
		t.Errorf("Updated: want 1, got %+v", result)
	}
}
