package parser

import "slices"

// SentenceBlocks lists the elements whose text splits into sentences. A run
// of them is one segment over their combined source range, so an element in
// a gap between two sentences (a <br/>) becomes one of its chips.
func (p XML) SentenceBlocks(original []byte) ([]SentenceBlock, error) {
	doc, err := p.parse(original)
	if err != nil {
		return nil, err
	}
	specs, err := collectXMLSegments(doc, segmenterOr(p.Segmenter))
	if err != nil {
		return nil, err
	}
	refs := buildReferenceIndex(doc)
	var out []SentenceBlock
	for _, sentences := range xmlBlocks(specs) {
		keys := make([]string, len(sentences))
		for i, s := range sentences {
			keys[i] = s.key
		}
		block := sentences[0].block
		out = append(out, SentenceBlock{
			Keys: keys,
			run: func(i, j int) (SentenceRange, error) {
				u := xmlRunContent(doc, refs, sentences, i, j)
				offsets := make([]int, j-i+1)
				for k := i; k <= j; k++ {
					if in := sentences[k].content.inlines; len(in) > 0 {
						offsets[k-i] = slices.Index(u.inlines, in[0])
					}
				}
				return SentenceRange{Key: numberedRangeKey(block, i, j), Text: u.plain, Rich: u.rich, Offsets: offsets}, nil
			},
			locate: locateNumbered(block, len(keys)),
		})
	}
	return out, nil
}

// xmlBlocks groups the specs of each element that splits into sentences, in
// order.
func xmlBlocks(specs []xmlSegmentSpec) [][]xmlSegmentSpec {
	var out [][]xmlSegmentSpec
	for i := 0; i < len(specs); {
		if specs[i].block == "" {
			i++
			continue
		}
		j := i
		for j < len(specs) && specs[j].block == specs[i].block {
			j++
		}
		out = append(out, specs[i:j])
		i = j
	}
	return out
}

// xmlRunContent is sentences i..j of one element as one segment: their
// combined source range and everything inline in it.
func xmlRunContent(doc *xmlDoc, refs referenceIndex, sentences []xmlSegmentSpec, i, j int) xmlContent {
	rng := TextRange{Start: sentences[i].rng.Start, End: sentences[j].rng.End}
	return buildXMLContent(doc.src, rng, sentences[i].node, refs, doc.rules)
}

// xmlRunEdits writes each translation stored under a range key over its run
// of sentences, and reports which specs it covered.
func xmlRunEdits(doc *xmlDoc, refs referenceIndex, specs []xmlSegmentSpec, translations map[string]string, refContent func(*xmlNode) (string, bool)) ([]spliceEdit, map[int]bool) {
	ranges := numberedRanges(translations)
	edits := make([]spliceEdit, 0, len(specs))
	joined := map[int]bool{}
	if len(ranges) == 0 {
		return edits, joined
	}
	offset := 0
	for _, sentences := range xmlBlocks(specs) {
		for offset < len(specs) && specs[offset].key != sentences[0].key {
			offset++
		}
		for start, r := range runsByStart(ranges[sentences[0].block], len(sentences)) {
			t := translations[r.key]
			if t == "" {
				continue
			}
			u := xmlRunContent(doc, refs, sentences, start, r.j)
			edits = append(edits, spliceEdit{
				rng:  TextRange{Start: sentences[start].rng.Start, End: sentences[r.j].rng.End},
				text: xmlChipsToMarkup(t, u.inlines, doc.src, refContent),
			})
			for k := start; k <= r.j; k++ {
				joined[offset+k] = true
			}
		}
	}
	return edits, joined
}
