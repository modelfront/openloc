package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// A join in the CAT editor puts consecutive sentences of one block (a
// paragraph, an XML element, a PowerPoint paragraph, an XLIFF trans-unit's
// <mrk> sentences) back into one segment. Its translation is stored under a
// range key, and Reconstruct writes it over those sentences and the text
// between them; the block's other sentences keep their own translations.

// SentenceRange is a run of consecutive sentences of one block as one
// segment: the key Reconstruct reads its translation under, its source, and
// how each sentence's own chip ids map into it.
type SentenceRange struct {
	Key  string
	Text string
	// Rich is the source with its chips, "" when it holds none, numbered as
	// Reconstruct reads a translation under Key.
	Rich string
	// Offsets[k] is added to the chip ids of the run's k-th sentence,
	// numbered on its own, to number them as in Rich.
	Offsets []int
}

// SentenceBlock is a block of a file that splits into two or more sentences.
type SentenceBlock struct {
	// Keys are the sentences' keys, in order.
	Keys []string
	run  func(i, j int) (SentenceRange, error)
	// locate returns the run a sentence or range key names.
	locate func(key string) (i, j int, ok bool)
}

// Range is sentences i..j (0-based, both included) as one segment. The error
// is ErrSentenceRange when the format cannot write them as one.
func (b SentenceBlock) Range(i, j int) (SentenceRange, error) {
	if i < 0 || j >= len(b.Keys) || i >= j {
		return SentenceRange{}, ErrSentenceRange
	}
	return b.run(i, j)
}

// Locate returns the sentences a key names: one sentence's key, or a range
// key of this block.
func (b SentenceBlock) Locate(key string) (i, j int, ok bool) {
	return b.locate(key)
}

// SentenceJoiner is a parser whose multi-sentence blocks a join can put back
// together.
type SentenceJoiner interface {
	SentenceBlocks(original []byte) ([]SentenceBlock, error)
}

// ErrSentenceRange is a run of sentences a format cannot write as one.
var ErrSentenceRange = errors.New("parser: these sentences cannot be joined")

// numberedRangeKey is the range key of formats whose sentence keys are
// "<block>#<n>": "<block>#<first>-<last>", 1-based like the sentence keys.
func numberedRangeKey(block string, i, j int) string {
	return fmt.Sprintf("%s#%d-%d", block, i+1, j+1)
}

var numberedRangeRe = regexp.MustCompile(`^(.*)#(\d+)-(\d+)$`)

// numberedRanges finds the range keys among translations, by block: block key
// to the 0-based runs, each with its translation's key.
func numberedRanges(translations map[string]string) map[string][]keyedRun {
	var out map[string][]keyedRun
	for k := range translations {
		m := numberedRangeRe.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		i, err1 := strconv.Atoi(m[2])
		j, err2 := strconv.Atoi(m[3])
		if err1 != nil || err2 != nil || i < 1 || j <= i {
			continue
		}
		if out == nil {
			out = map[string][]keyedRun{}
		}
		out[m[1]] = append(out[m[1]], keyedRun{key: k, i: i - 1, j: j - 1})
	}
	return out
}

// keyedRun is sentences i..j of a block and the key of their translation.
type keyedRun struct {
	key  string
	i, j int
}

// runsByStart maps each run's first sentence to the run, dropping runs that
// leave the block's n sentences or overlap an earlier run.
func runsByStart(runs []keyedRun, n int) map[int]keyedRun {
	out := map[int]keyedRun{}
	covered := make([]bool, n)
	for _, r := range runs {
		if r.j >= n {
			continue
		}
		free := true
		for k := r.i; k <= r.j; k++ {
			free = free && !covered[k]
		}
		if !free {
			continue
		}
		for k := r.i; k <= r.j; k++ {
			covered[k] = true
		}
		out[r.i] = r
	}
	return out
}

// locateNumbered is SentenceBlock.locate for "<block>#<n>" sentence keys.
func locateNumbered(block string, n int) func(string) (int, int, bool) {
	return func(key string) (int, int, bool) {
		for k := 0; k < n; k++ {
			if key == fmt.Sprintf("%s#%d", block, k+1) {
				return k, k, true
			}
		}
		if m := numberedRangeRe.FindStringSubmatch(key); m != nil && m[1] == block {
			i, _ := strconv.Atoi(m[2])
			j, _ := strconv.Atoi(m[3])
			if i >= 1 && j > i && j <= n {
				return i - 1, j - 1, true
			}
		}
		return 0, 0, false
	}
}

// SentenceBlocks lists the blocks that split into sentences.
func (s sentenceSegmenter) SentenceBlocks(original []byte) ([]SentenceBlock, error) {
	blocks, err := s.inner.Parse(original)
	if err != nil {
		return nil, err
	}
	plans, err := sentencePlans(segmenterOr(s.seg), blocks)
	if err != nil {
		return nil, err
	}
	var out []SentenceBlock
	for _, b := range blocks {
		plan, ok := plans[b.Key]
		if !ok {
			continue
		}
		keys := make([]string, len(plan.parts))
		for i := range plan.parts {
			keys[i] = sentenceKey(b.Key, i)
		}
		key := b.Key
		out = append(out, SentenceBlock{
			Keys: keys,
			run: func(i, j int) (SentenceRange, error) {
				r := plan.run(i, j)
				r.Key = numberedRangeKey(key, i, j)
				return r, nil
			},
			locate: locateNumbered(key, len(keys)),
		})
	}
	return out, nil
}

// run is sentences i..j of the plan as one segment, with the gaps between
// them. Its chips are numbered from 1; Reconstruct reads a translation of it
// the same way.
func (plan sentencePlan) run(i, j int) SentenceRange {
	text, rich := plan.joinParts(i, j)
	base := 0
	if rich != "" {
		rich, base = renumberChipIDs(rich)
	}
	offsets := make([]int, j-i+1)
	for k := i; k <= j; k++ {
		offsets[k-i] = plan.parts[k].idBase - base
	}
	return SentenceRange{Text: text, Rich: rich, Offsets: offsets}
}

// joinParts is sentences i..j of the plan with the gaps between them: the
// plain text, and the rich text in the block's chip numbering ("" when the
// block has no chip).
func (plan sentencePlan) joinParts(i, j int) (text, rich string) {
	gaps := plan.plainGaps
	if len(plan.richGaps) > 0 {
		gaps = plan.richGaps
	}
	pieces := make([]Piece, 0, j-i+1)
	for k := i; k <= j; k++ {
		p := plan.parts[k]
		piece := Piece{Text: p.text, Rich: p.rich, ChipBase: p.idBase}
		if k > i {
			piece.Gap = gaps[k]
		}
		pieces = append(pieces, piece)
	}
	return JoinPieces(pieces)
}
