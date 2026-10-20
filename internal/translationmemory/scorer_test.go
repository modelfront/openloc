package translationmemory

import (
	"strings"
	"testing"
)

func TestLevenshteinScorer_Contract(t *testing.T) {
	s := LevenshteinScorer{}

	t.Run("byte_identical_returns_one", func(t *testing.T) {
		score, ok := s.Score("Hello world", "Hello world")
		if !ok || score != 1.0 {
			t.Errorf("score, ok = %v, %v; want 1.0, true", score, ok)
		}
	})

	// Punctuation/case/whitespace-only differences are a "nearly exact" match.
	// Score returns RAW similarity for them and IsNearExact flags them; the
	// 99% band is Penalties.Apply's job (see
	// TestPenalties_Apply/near_exact_scores_from_one_not_raw).
	t.Run("near_exact_returns_raw_similarity_and_is_flagged", func(t *testing.T) {
		cases := []struct{ name, query, source string }{
			{"trailing_period", "Hello world.", "Hello world"},
			{"case_diff_all_lower", "hello world", "Hello world"},
			{"trailing_whitespace", "Hello world ", "Hello world"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				score, ok := s.Score(c.query, c.source)
				// Raw edit distance on a short segment — the very cliff the band
				// exists to correct, so it must be visibly below 0.99 here.
				if !ok || score >= 0.99 || score <= 0.8 {
					t.Errorf("score, ok = %v, %v; want 0.8 < raw < 0.99, true", score, ok)
				}
				if !IsNearExact(c.query, c.source) {
					t.Errorf("IsNearExact(%q, %q) = false; the band would never be applied", c.query, c.source)
				}
			})
		}
	})

	// Control + anti-blanket guard: a genuine one-word swap must stay in
	// graduated fuzzy, NOT get promoted to the 0.99 near-exact band. pg_trgm
	// scores ~0.52; Levenshtein 1 - 7/23 ≈ 0.696.
	t.Run("one_word_swap_stays_graduated_fuzzy", func(t *testing.T) {
		score, ok := s.Score("This text is in italics", "This text is in bold")
		if !ok || score <= 0.65 || score >= 0.99 {
			t.Errorf("score = %v, ok = %v; want 0.65 < score < 0.99, true", score, ok)
		}
	})

	t.Run("empty_both_returns_one", func(t *testing.T) {
		score, ok := s.Score("", "")
		if !ok || score != 1.0 {
			t.Errorf("score, ok = %v, %v; want 1.0, true", score, ok)
		}
	})

	t.Run("empty_one_side_returns_zero", func(t *testing.T) {
		score, ok := s.Score("", "Hello")
		if !ok || score != 0.0 {
			t.Errorf("score, ok = %v, %v; want 0.0, true", score, ok)
		}
	})

	t.Run("over_length_cap_declines", func(t *testing.T) {
		long := strings.Repeat("a", MaxScoreLength+1)
		if _, ok := s.Score(long, "short"); ok {
			t.Errorf("ok = true for query > cap, want false")
		}
		if _, ok := s.Score("short", long); ok {
			t.Errorf("ok = true for source > cap, want false")
		}
	})

	t.Run("at_length_cap_still_scored", func(t *testing.T) {
		exact := strings.Repeat("a", MaxScoreLength)
		score, ok := s.Score(exact, exact)
		if !ok || score != 1.0 {
			t.Errorf("score, ok = %v, %v at len = cap; want 1.0, true", score, ok)
		}
	})

	// Pins rune-based length normalization — bytes-based would silently shift
	// this since "é" is 2 bytes ("café" vs "cafe" = 1 rune of 4 → 0.75).
	t.Run("multibyte_runes_scored_on_runes_not_bytes", func(t *testing.T) {
		score, ok := s.Score("café", "cafe")
		if !ok || score >= 1.0 || score < 0.5 {
			t.Errorf("score = %v, ok = %v; want 0.5 <= score < 1", score, ok)
		}
	})
}

var _ Scorer = LevenshteinScorer{}

// ScoreToPercent must ROUND, not truncate, so the stored match % (Match
// column) agrees with the TM-matches sidebar, which rounds the same float in
// JS. Truncation turns 0.739 into 73 (Match column) next to 74 in the panel.
// Reverting ScoreToPercent to int(score*100) turns the 0.739 and 0.678 cases
// RED.
func TestScoreToPercent_RoundsNotTruncates(t *testing.T) {
	cases := []struct {
		score float64
		want  int16
	}{
		{0.739, 74}, // truncation gives 73 — the status-vs-panel mismatch
		{0.678, 68}, // 67.8 rounds up
		{0.674, 67}, // 67.4 rounds down
		{0.735, 74}, // exact .5 rounds away from zero
		{0.71, 71},  // clean value unaffected
		{1.0, 100},  // exact match
		{1.5, 100},  // clamp above 100 (defensive)
		{-0.1, 0},   // clamp below 0 (defensive)
	}
	for _, c := range cases {
		if got := ScoreToPercent(c.score); got != c.want {
			t.Errorf("ScoreToPercent(%v) = %d, want %d", c.score, got, c.want)
		}
	}
}
