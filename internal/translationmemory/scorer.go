package translationmemory

import (
	"math"
	"strings"
	"unicode"

	"openloc/internal/strdist"
)

// Scorer returns RAW textual similarity. Banding and penalties are not its job
// — the caller passes the result through Penalties.Apply, which is where the
// near-exact band and every deduction live (they're per-project configuration,
// and a Scorer is constructed once).
//
// ok=false means declined (e.g. inputs above the length cap); caller must
// fall back to the stage-1 score and ignore the returned score.
type Scorer interface {
	Score(query, source string) (score float64, ok bool)
}

// Bounds the O(n·m) Levenshtein matrix at ~4M cells.
const MaxScoreLength = 2048

// Low pg_trgm cutoff for recall; the project's fuzzy_threshold filters AFTER
// rescore. Raising this silently drops short-sentence fuzzy candidates.
//
// Penalties don't need this lowered: it's a flat floor well below any display
// threshold, not a value derived from one, and the penalised comparison happens
// in Go after the rescore.
const FindRecallThreshold float32 = 0.3

type LevenshteinScorer struct{}

func (LevenshteinScorer) Score(query, source string) (float64, bool) {
	if len(query) > MaxScoreLength || len(source) > MaxScoreLength {
		return 0, false
	}
	if query == source {
		return 1.0, true
	}
	qr := []rune(query)
	sr := []rune(source)
	maxLen := len(qr)
	if len(sr) > maxLen {
		maxLen = len(sr)
	}
	if maxLen == 0 {
		return 1.0, true
	}
	dist := strdist.Levenshtein(query, source)
	return 1.0 - float64(dist)/float64(maxLen), true
}

// IsNearExact reports whether two strings differ ONLY in punctuation, case or
// whitespace — the same segment to a human, but not byte-identical. Byte-equal
// input is not "near" exact, it IS exact: reporting true for it would dock a
// genuine 100% match by the near-exact penalty.
//
// Callers put the result on MatchInfo.NearExact; Penalties.Apply turns it into
// the 99%-ish band.
func IsNearExact(query, source string) bool {
	return query != source && normalizeNearExact(query) == normalizeNearExact(source)
}

// Internal punctuation is left in: "Woman, without her man" vs "Woman without
// her man" can flip meaning, so it stays a graduated fuzzy rather than 99%.
func normalizeNearExact(s string) string {
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
}

// ScoreToPercent converts a [0,1] similarity score into the integer match
// percent stored in tm_match_percent and shown in the editor's Match column.
// It rounds, as the Resources panel rounds the same float in JS (Math.round),
// so 0.739 reads 74% in both places. Clamped to [0,100]: 101 is the
// in-context exact match, which only the pipeline assigns.
func ScoreToPercent(score float64) int16 {
	return int16(min(max(int(math.Round(score*100)), 0), 100))
}
