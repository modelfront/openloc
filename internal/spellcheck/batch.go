package spellcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"openloc/internal/errtrack"
)

// Concurrency ceiling for a batch check. The self-hosted server is CPU-bound
// per request; more parallelism than this just queues inside the JVM while
// holding our goroutines and HTTP connections open.
const batchConcurrency = 8

// A batch check joins many segments into few LanguageTool requests: LT's
// per-request overhead dominates for short CAT segments, so 25 segments in one
// request cost far less than 25 requests. Segments are joined with a paragraph
// break so sentence- and paragraph-level rules can't leak across segments, and
// matches are split back per segment by offset.
const (
	joinSeparator = "\n\n"
	// Caps per joined request. Segment count keeps one failure from sinking too
	// many segments; the unit cap (UTF-16 code units, LT's offset unit) keeps a
	// single request from serializing the whole batch behind one slow check.
	maxGroupSegments = 25
	maxGroupUnits    = 10000
	// Floor per joined request (unit cap still splits): LT is CPU-bound, so
	// fuller requests beat parallelism. Tradeoff: one failure sinks the group.
	minGroupSegments = 10
)

// BatchItem pairs an identifier with the text to check.
type BatchItem struct {
	ID   int64
	Text string
}

// BatchResult is one item's outcome. Err is per-item so a single bad segment
// doesn't sink the whole run.
type BatchResult struct {
	ID      int64
	Matches []Match
	Err     error
}

// CheckBatch checks many texts, preserving input order. Cache-missed segments
// are joined into few LanguageTool requests (see joinSeparator) which run
// concurrently; matches come back attributed to their segment with
// segment-local offsets. A failed request errors only its own group's
// segments.
func (c *Client) CheckBatch(ctx context.Context, items []BatchItem, language string) []BatchResult {
	results := make([]BatchResult, len(items))
	for i, item := range items {
		results[i] = BatchResult{ID: item.ID}
	}

	lang := toLTLanguage(language)
	if lang == "" {
		err := fmt.Errorf("spellcheck: empty language code")
		for i := range results {
			results[i].Err = err
		}
		return results
	}

	// Cache pass — repeated segments and re-runs after a partial fix are common
	// enough in a document-wide check that this measurably cuts the work.
	var missed []int
	for i, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		if cached, ok := c.cache.get(cacheKey(lang, item.Text)); ok {
			results[i].Matches = cached
			continue
		}
		missed = append(missed, i)
	}

	sem := make(chan struct{}, batchConcurrency)
	var wg sync.WaitGroup
	for _, group := range groupBatch(items, missed) {
		wg.Add(1)
		go func(group []int) {
			// First deferred call, so it also catches a panic raised by the
			// defers below. A panic here is not recoverable by CheckBatch's
			// caller — it kills the process — so this is the only way it gets
			// reported.
			defer errtrack.Monitor(ctx, "spellcheck", "check-batch")
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				for _, i := range group {
					results[i].Err = ctx.Err()
				}
				return
			}
			c.checkGroup(ctx, items, group, lang, results)
		}(group)
	}

	wg.Wait()
	return results
}

// checkGroup runs one joined LanguageTool request for the items at `group`
// indexes and writes each item's outcome into `results` (disjoint indexes per
// group, so no locking).
func (c *Client) checkGroup(ctx context.Context, items []BatchItem, group []int, lang string, results []BatchResult) {
	texts := make([]string, len(group))
	for gi, i := range group {
		texts[gi] = items[i].Text
	}

	matches, err := c.doCheck(ctx, strings.Join(texts, joinSeparator), lang)
	if err != nil {
		for _, i := range group {
			results[i].Err = err
		}
		return
	}

	perSegment := splitJoinedMatches(texts, matches)
	for gi, i := range group {
		kept := filterMatches(perSegment[gi])
		c.cache.set(cacheKey(lang, items[i].Text), kept)
		results[i].Matches = kept
	}
}

// groupBatch partitions the missed indexes into joined-request groups: it
// targets batchConcurrency groups, clamped to
// [minGroupSegments, maxGroupSegments]; the unit budget still splits regardless.
func groupBatch(items []BatchItem, missed []int) [][]int {
	if len(missed) == 0 {
		return nil
	}
	targetSize := (len(missed) + batchConcurrency - 1) / batchConcurrency
	if targetSize < minGroupSegments {
		targetSize = minGroupSegments
	}
	if targetSize > maxGroupSegments {
		targetSize = maxGroupSegments
	}

	var groups [][]int
	var cur []int
	units := 0
	for _, i := range missed {
		u := utf16Length(items[i].Text)
		if len(cur) > 0 && (len(cur) >= targetSize || units+u > maxGroupUnits) {
			groups = append(groups, cur)
			cur, units = nil, 0
		}
		cur = append(cur, i)
		units += u
	}
	return append(groups, cur)
}

// splitJoinedMatches maps matches on the joined text back to per-segment
// matches with segment-local offsets. All arithmetic is in UTF-16 code units —
// LanguageTool's offset unit. A match that crosses a segment boundary can only
// be an artifact of the joining (no rule we keep spans a paragraph break), so
// it is dropped rather than mis-attributed.
func splitJoinedMatches(texts []string, matches []Match) [][]Match {
	sepUnits := utf16Length(joinSeparator)
	starts := make([]int, len(texts))
	lengths := make([]int, len(texts))
	pos := 0
	for i, t := range texts {
		if i > 0 {
			pos += sepUnits
		}
		starts[i] = pos
		lengths[i] = utf16Length(t)
		pos += lengths[i]
	}

	out := make([][]Match, len(texts))
	for _, m := range matches {
		i := sort.Search(len(starts), func(j int) bool { return starts[j] > m.Offset }) - 1
		if i < 0 || m.Offset+m.Length > starts[i]+lengths[i] {
			continue
		}
		local := m
		local.Offset = m.Offset - starts[i]
		out[i] = append(out[i], local)
	}
	return out
}

func utf16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}
