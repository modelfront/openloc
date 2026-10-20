package job

import (
	"testing"

	db "openloc/internal/database/sqlcgen"
)

func TestAggregateJobAnalysis_TMAndNewBreakdown(t *testing.T) {
	rows := []db.GetJobAnalysisRow{
		{Origin: "tm", PredictOutcome: "none", MatchBand: "100", SegmentCount: 3, WordCount: 30, CharCount: 150},
		{Origin: "tm", PredictOutcome: "none", MatchBand: "95-99", SegmentCount: 2, WordCount: 20, CharCount: 100},
		{Origin: "tm", PredictOutcome: "none", MatchBand: "85-94", SegmentCount: 1, WordCount: 10, CharCount: 50},
		{Origin: "tm", PredictOutcome: "none", MatchBand: "75-84", SegmentCount: 1, WordCount: 8, CharCount: 40},
		// MT segments without QP approval are "New" (no TM match)
		{Origin: "mt", PredictOutcome: "none", MatchBand: "no-match", SegmentCount: 5, WordCount: 60, CharCount: 300},
		{Origin: "unknown", PredictOutcome: "none", MatchBand: "no-match", SegmentCount: 2, WordCount: 15, CharCount: 80},
	}

	resp := aggregateJobAnalysis(rows)

	assertBand(t, "tm_exact", resp.TMExact, 3, 30, 150)
	assertBand(t, "tm_fuzzy_95", resp.TMFuzzy95, 2, 20, 100)
	assertBand(t, "tm_fuzzy_85", resp.TMFuzzy85, 1, 10, 50)
	assertBand(t, "tm_fuzzy_75", resp.TMFuzzy75, 1, 8, 40)
	// MT + unknown both go to "New" (no TM leverage)
	assertBand(t, "new", resp.New, 7, 75, 380)
	assertBand(t, "total", resp.Total, 14, 143, 720)
}

func TestAggregateJobAnalysis_QPBreakdown(t *testing.T) {
	rows := []db.GetJobAnalysisRow{
		{Origin: "mt", PredictOutcome: "approved", MatchBand: "no-match", SegmentCount: 4, WordCount: 40, CharCount: 200},
		{Origin: "mt", PredictOutcome: "ape_approved", MatchBand: "no-match", SegmentCount: 2, WordCount: 25, CharCount: 130},
		{Origin: "mt", PredictOutcome: "ape_needs_review", MatchBand: "no-match", SegmentCount: 3, WordCount: 30, CharCount: 160},
		{Origin: "mt", PredictOutcome: "rejected", MatchBand: "no-match", SegmentCount: 1, WordCount: 10, CharCount: 50},
		{Origin: "tm", PredictOutcome: "none", MatchBand: "100", SegmentCount: 5, WordCount: 50, CharCount: 250},
	}

	resp := aggregateJobAnalysis(rows)

	// QE band = QP approved + APE approved
	assertBand(t, "qe", resp.QE, 6, 65, 330)
	// New = MT segments not approved by QP (ape_needs_review + rejected)
	assertBand(t, "new", resp.New, 4, 40, 210)
	// TM exact
	assertBand(t, "tm_exact", resp.TMExact, 5, 50, 250)

	// QP breakdown stays the same
	assertBand(t, "qp.approved", resp.QP.Approved, 4, 40, 200)
	assertBand(t, "qp.ape_approved", resp.QP.ApeApproved, 2, 25, 130)
	assertBand(t, "qp.ape_needs_review", resp.QP.ApeNeedsReview, 3, 30, 160)
	assertBand(t, "qp.rejected", resp.QP.Rejected, 1, 10, 50)
	assertBand(t, "qp.not_run", resp.QP.NotRun, 5, 50, 250)
	// saved_from_review = approved + ape_approved
	assertBand(t, "qp.saved_from_review", resp.QP.SavedFromReview, 6, 65, 330)
}

func TestAggregateJobAnalysis_Empty(t *testing.T) {
	resp := aggregateJobAnalysis(nil)
	assertBand(t, "total", resp.Total, 0, 0, 0)
}

func assertBand(t *testing.T, name string, b MatchBand, segments, words, chars int32) {
	t.Helper()
	if b.Segments != segments {
		t.Errorf("%s.segments: got %d, want %d", name, b.Segments, segments)
	}
	if b.Words != words {
		t.Errorf("%s.words: got %d, want %d", name, b.Words, words)
	}
	if b.Characters != chars {
		t.Errorf("%s.characters: got %d, want %d", name, b.Characters, chars)
	}
}
