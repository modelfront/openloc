package translationmemory

import (
	"crypto/md5"
	"encoding/hex"

	db "openloc/internal/database/sqlcgen"
)

// One scoring path for a TM candidate, shared by pre-translation and the
// editor's TM panel, so the Match column and the panel card show the same
// percentage for the same entry.

// DefaultFuzzyThreshold applies when the settings carry a negative threshold.
const DefaultFuzzyThreshold float32 = 0.75

// FuzzyThreshold is the minimum rescored score a fuzzy candidate needs.
func FuzzyThreshold(configured float32) float32 {
	if configured < 0 {
		return DefaultFuzzyThreshold
	}
	return configured
}

// ContextHash is how a TM entry records a neighbouring source segment for
// in-context (101%) matching: the lowercase hex md5, equal to Postgres
// md5(text). A document boundary hashes the empty string.
func ContextHash(source string) string {
	sum := md5.Sum([]byte(source))
	return hex.EncodeToString(sum[:])
}

// NeighborContext is the ContextHash of a segment's previous and next source
// segments, in document order.
type NeighborContext struct{ Prev, Next string }

// NewNeighborContext hashes a segment's neighbouring source texts.
func NewNeighborContext(prevSource, nextSource string) NeighborContext {
	return NeighborContext{Prev: ContextHash(prevSource), Next: ContextHash(nextSource)}
}

// ExactPercent is the percentage of a clean exact match: 101 when the entry's
// stored neighbour hashes equal the segment's, else 100. An entry without
// stored context (TMX import, an old row) or a segment of unknown context
// (nil) never reaches 101.
func ExactPercent(prevHash, nextHash *string, segment *NeighborContext) int16 {
	if segment != nil && prevHash != nil && nextHash != nil &&
		*prevHash == segment.Prev && *nextHash == segment.Next {
		return 101
	}
	return 100
}

// HitScore rescores a recall candidate for query: the Scorer's similarity
// (the pg_trgm score when the Scorer declines an over-cap input), then the
// penalties.
func HitScore(scorer Scorer, p Penalties, query string, tmID int64, wantSrc, wantTgt string, hit db.FuzzyLookupTMRow) float64 {
	score, ok := scorer.Score(query, hit.SourceText)
	info := MatchInfo{
		TMID:    tmID,
		WantSrc: wantSrc, GotSrc: hit.SourceLanguage,
		WantTgt: wantTgt, GotTgt: hit.TargetLanguage,
		// A declined input fell back to pg_trgm; don't promote it into the
		// near-exact band.
		NearExact: ok && IsNearExact(query, hit.SourceText),
	}
	if !ok {
		score = float64(hit.Score)
	}
	return p.Apply(score, info)
}

// HitPercent is the Match-column percentage of a candidate HitScore rated
// score: a byte-identical source with no penalty is an exact match (100, or
// 101 in context), anything else ScoreToPercent.
func HitPercent(query string, hit db.FuzzyLookupTMRow, score float64, segment *NeighborContext) int16 {
	if hit.SourceText == query && score == 1 {
		return ExactPercent(hit.PrevSourceHash, hit.NextSourceHash, segment)
	}
	return ScoreToPercent(score)
}
