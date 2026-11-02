package parser

import (
	"context"
	"slices"
)

// SentenceBlocks lists the paragraphs and alt texts that split into
// sentences. A run of them is one sentence over their combined range, its
// chips numbered across it.
func (p PPTX) SentenceBlocks(original []byte) (_ []SentenceBlock, err error) {
	defer recoverPPTX(context.Background(), "pptx sentence blocks", &err)
	pkg, err := openPPTX(original)
	if err != nil {
		return nil, err
	}
	units, err := collectPPTX(pkg, segmenterOr(p.Segmenter))
	if err != nil {
		return nil, err
	}
	var out []SentenceBlock
	for _, u := range units {
		sentences := u.sentences()
		if len(sentences) < 2 {
			continue
		}
		keys := make([]string, len(sentences))
		for i := range sentences {
			keys[i] = u.sentenceKey(i)
		}
		out = append(out, SentenceBlock{
			Keys: keys,
			run: func(i, j int) (SentenceRange, error) {
				m := u.mergeSentences(sentences, i, j)
				offsets := make([]int, j-i+1)
				for k := i; k <= j; k++ {
					if c := sentences[k].chips; len(c) > 0 {
						offsets[k-i] = slices.Index(m.chips, c[0])
					}
				}
				return SentenceRange{Key: numberedRangeKey(u.key, i, j), Text: m.text, Rich: m.rich, Offsets: offsets}, nil
			},
			locate: locateNumbered(u.key, len(keys)),
		})
	}
	return out, nil
}

// mergeSentences is sentences i..j of the unit as one sentence over their
// combined range.
func (u *pptxUnit) mergeSentences(sentences []pptxSentence, i, j int) pptxSentence {
	rng := TextRange{Start: sentences[i].rng.Start, End: sentences[j].rng.End}
	if u.para != nil {
		return u.para.sentence(rng)
	}
	return pptxSentence{rng: rng, text: u.alt.value[rng.Start:rng.End]}
}

// withRuns returns the unit's sentences with each run that has a
// translation under its range key merged into one, and the translations
// lined up with them.
func (u *pptxUnit) withRuns(translations map[string]string, runs map[int]keyedRun) (sentences []pptxSentence, trans []string, has []bool) {
	src := u.sentences()
	for i := 0; i < len(src); i++ {
		key := u.sentenceKey(i)
		s := src[i]
		if r, ok := runs[i]; ok {
			key, s = r.key, u.mergeSentences(src, i, r.j)
			i = r.j
		}
		t := translations[key]
		sentences = append(sentences, s)
		trans = append(trans, t)
		has = append(has, t != "")
	}
	return sentences, trans, has
}

// mirrorRuns merges the same runs in a mirror paragraph, which shows the
// unit's paragraph and is split the same way.
func mirrorRuns(p *pptxParagraph, runs map[int]keyedRun) []pptxSentence {
	var out []pptxSentence
	for i := 0; i < len(p.sentences); i++ {
		if r, ok := runs[i]; ok && r.j < len(p.sentences) {
			out = append(out, p.sentence(TextRange{Start: p.sentences[i].rng.Start, End: p.sentences[r.j].rng.End}))
			i = r.j
			continue
		}
		out = append(out, p.sentences[i])
	}
	return out
}
