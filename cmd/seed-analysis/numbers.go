package main

import (
	"hash/fnv"
	"math"

	db "openloc/internal/database/sqlcgen"
)

// sourceFile is a file's size in source words and how long its segments are.
type sourceFile struct {
	words       int
	wordsPerSeg float64
	// repetitions is the share of words that repeat inside the project: high
	// for UI strings ("Save", "Cancel"), low for release notes.
	repetitions float64
}

var sourceFiles = map[string]sourceFile{
	"product-manual.txt":     {words: 12480, wordsPerSeg: 14.2, repetitions: 0.11},
	"release-notes-4.2.txt":  {words: 2150, wordsPerSeg: 16.5, repetitions: 0.03},
	"ui-strings.txt":         {words: 3870, wordsPerSeg: 4.3, repetitions: 0.24},
	"marketing-brochure.txt": {words: 1640, wordsPerSeg: 18.1, repetitions: 0.02},
	"support-faq.txt":        {words: 5320, wordsPerSeg: 15.0, repetitions: 0.09},
}

// Band order in the shares below.
const (
	b101 = iota
	b100
	b8599
	b6584
	bNew
	nBands
)

// leverage is a language's TM maturity: the share of the non-repeated words
// that land in each band, and how much of each band the quality check
// approves without a person.
type leverage struct {
	share       [nBands]float64
	autoApprove [nBands]float64
}

var languages = map[string]leverage{
	"de": {share: [nBands]float64{0.19, 0.24, 0.13, 0.08, 0.36}, autoApprove: [nBands]float64{0.95, 0.70, 0.30, 0.12, 0.22}},
	"fr": {share: [nBands]float64{0.16, 0.22, 0.14, 0.09, 0.39}, autoApprove: [nBands]float64{0.95, 0.65, 0.28, 0.10, 0.20}},
	"es": {share: [nBands]float64{0.11, 0.17, 0.13, 0.10, 0.49}, autoApprove: [nBands]float64{0.92, 0.60, 0.25, 0.08, 0.18}},
	"it": {share: [nBands]float64{0.07, 0.13, 0.11, 0.10, 0.59}, autoApprove: [nBands]float64{0.90, 0.55, 0.20, 0.06, 0.14}},
	"ja": {share: [nBands]float64{0.01, 0.05, 0.06, 0.08, 0.80}, autoApprove: [nBands]float64{0.80, 0.40, 0.10, 0.03, 0.05}},
}

// Weights, as a share of a full new word, for the weighted word columns.
var (
	weights          = [nBands]float64{0, 0.10, 0.40, 0.70, 1}
	repetitionWeight = 0.10
	repetitionAA     = 0.35
	// English averages about 5.9 characters a word, counting the space.
	charsPerWord = 5.9
)

// band is one band's counts and their auto-approved subset.
type band struct {
	segments, words, characters, weighted         int32
	segmentsAA, wordsAA, charactersAA, weightedAA int32
}

// counts sizes a band of words with that file's segment length.
func counts(words int, f sourceFile, weight, autoApprove float64) band {
	segs := int(math.Round(float64(words) / f.wordsPerSeg))
	if words > 0 && segs == 0 {
		segs = 1
	}
	wordsAA := int(math.Round(float64(words) * autoApprove))
	segsAA := int(math.Round(float64(segs) * autoApprove))
	return band{
		segments: int32(segs), words: int32(words),
		characters: int32(math.Round(float64(words) * charsPerWord)), weighted: int32(math.Round(float64(words) * weight)),
		segmentsAA: int32(segsAA), wordsAA: int32(wordsAA),
		charactersAA: int32(math.Round(float64(wordsAA) * charsPerWord)), weightedAA: int32(math.Round(float64(wordsAA) * weight)),
	}
}

// jitter is a fixed factor in [0.85, 1.15] per file, language and band, so no
// two rows are the same shares of different totals.
func jitter(file, lang string, b int) float64 {
	h := fnv.New32a()
	h.Write([]byte(file + "|" + lang + "|" + string(rune('a'+b)))) //nolint:errcheck // hash.Hash.Write never returns an error
	return 0.85 + 0.30*float64(h.Sum32()%1000)/999
}

// breakdown is one job_analysis row: the file's words split across the bands
// by the language's leverage. The bands add up to the file's word count.
func breakdown(file, lang string) db.InsertJobAnalysisParams {
	f, l := sourceFiles[file], languages[lang]
	reps := int(math.Round(float64(f.words) * f.repetitions))
	rest := f.words - reps

	var share [nBands]float64
	var sum float64
	for b := range share {
		share[b] = l.share[b] * jitter(file, lang, b)
		sum += share[b]
	}
	var words [nBands]int
	used := 0
	for b := 0; b < bNew; b++ {
		words[b] = int(math.Round(float64(rest) * share[b] / sum))
		used += words[b]
	}
	words[bNew] = rest - used

	var bands [nBands]band
	for b := range bands {
		bands[b] = counts(words[b], f, weights[b], l.autoApprove[b])
	}
	r := counts(reps, f, repetitionWeight, repetitionAA)

	return db.InsertJobAnalysisParams{
		TargetLanguage: lang, JobNameSnapshot: file,

		Tm101Segments: bands[b101].segments, Tm101Words: bands[b101].words, Tm101Characters: bands[b101].characters, Tm101WeightedWords: bands[b101].weighted,
		Tm101SegmentsAutoApproved: bands[b101].segmentsAA, Tm101WordsAutoApproved: bands[b101].wordsAA, Tm101CharactersAutoApproved: bands[b101].charactersAA, Tm101WeightedWordsAutoApproved: bands[b101].weightedAA,

		Tm100Segments: bands[b100].segments, Tm100Words: bands[b100].words, Tm100Characters: bands[b100].characters, Tm100WeightedWords: bands[b100].weighted,
		Tm100SegmentsAutoApproved: bands[b100].segmentsAA, Tm100WordsAutoApproved: bands[b100].wordsAA, Tm100CharactersAutoApproved: bands[b100].charactersAA, Tm100WeightedWordsAutoApproved: bands[b100].weightedAA,

		Tm8599Segments: bands[b8599].segments, Tm8599Words: bands[b8599].words, Tm8599Characters: bands[b8599].characters, Tm8599WeightedWords: bands[b8599].weighted,
		Tm8599SegmentsAutoApproved: bands[b8599].segmentsAA, Tm8599WordsAutoApproved: bands[b8599].wordsAA, Tm8599CharactersAutoApproved: bands[b8599].charactersAA, Tm8599WeightedWordsAutoApproved: bands[b8599].weightedAA,

		Tm6584Segments: bands[b6584].segments, Tm6584Words: bands[b6584].words, Tm6584Characters: bands[b6584].characters, Tm6584WeightedWords: bands[b6584].weighted,
		Tm6584SegmentsAutoApproved: bands[b6584].segmentsAA, Tm6584WordsAutoApproved: bands[b6584].wordsAA, Tm6584CharactersAutoApproved: bands[b6584].charactersAA, Tm6584WeightedWordsAutoApproved: bands[b6584].weightedAA,

		NewSegments: bands[bNew].segments, NewWords: bands[bNew].words, NewCharacters: bands[bNew].characters, NewWeightedWords: bands[bNew].weighted,
		NewSegmentsAutoApproved: bands[bNew].segmentsAA, NewWordsAutoApproved: bands[bNew].wordsAA, NewCharactersAutoApproved: bands[bNew].charactersAA, NewWeightedWordsAutoApproved: bands[bNew].weightedAA,

		RepetitionsSegments: r.segments, RepetitionsWords: r.words, RepetitionsCharacters: r.characters, RepetitionsWeightedWords: r.weighted,
		RepetitionsSegmentsAutoApproved: r.segmentsAA, RepetitionsWordsAutoApproved: r.wordsAA, RepetitionsCharactersAutoApproved: r.charactersAA, RepetitionsWeightedWordsAutoApproved: r.weightedAA,
	}
}
