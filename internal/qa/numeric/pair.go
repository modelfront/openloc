package numeric

// pairing is how the dates (or times, or quantities) of the two sides match.
type pairing struct {
	srcOK, tgtOK []bool
	// mismatch is a source and a target entity that are the only ones left
	// unmatched on their side: the target wrote that one differently.
	// -1 when there is no such pair.
	mismatch [2]int
}

// pair matches every source entity against every target entity: an entity
// is matched when any entity of the other side matches it, so one target
// date may stand for the same date written twice in the source. Weak
// entities take part only through a match.
func pair(src, tgt []candidate, match func(s, t candidate) bool) pairing {
	p := pairing{srcOK: make([]bool, len(src)), tgtOK: make([]bool, len(tgt)), mismatch: [2]int{-1, -1}}
	for i, s := range src {
		for j, t := range tgt {
			if match(s, t) {
				p.srcOK[i], p.tgtOK[j] = true, true
			}
		}
	}
	p.mismatch = loneMismatch(src, tgt, p, func(candidate, candidate) bool { return true })
	return p
}

// loneMismatch returns the one unmatched strong source entity and the one
// unmatched strong target entity that same(s, t) allows to be compared, when
// each is the only one of its side.
func loneMismatch(src, tgt []candidate, p pairing, same func(s, t candidate) bool) [2]int {
	si, ti := -1, -1
	for i, s := range src {
		if p.srcOK[i] || s.weak {
			continue
		}
		if si >= 0 {
			return [2]int{-1, -1}
		}
		si = i
	}
	for j, t := range tgt {
		if p.tgtOK[j] || t.weak {
			continue
		}
		if ti >= 0 {
			return [2]int{-1, -1}
		}
		ti = j
	}
	if si < 0 || ti < 0 || !same(src[si], tgt[ti]) {
		return [2]int{-1, -1}
	}
	return [2]int{si, ti}
}

func datesMatch(src, tgt *side) func(s, t candidate) bool {
	return func(s, t candidate) bool {
		if src.t.norm[s.start:s.end] == tgt.t.norm[t.start:t.end] {
			return true
		}
		for _, a := range s.dates {
			for _, b := range t.dates {
				if a.satisfiedBy(b) {
					return true
				}
			}
		}
		return false
	}
}

func timesMatch(src, tgt *side) func(s, t candidate) bool {
	return func(s, t candidate) bool {
		if src.t.norm[s.start:s.end] == tgt.t.norm[t.start:t.end] {
			return true
		}
		for _, a := range s.minutes {
			for _, b := range t.minutes {
				if a == b {
					return true
				}
			}
		}
		return false
	}
}

func quantitiesMatch(s, t candidate) bool {
	a, b := s.qty, t.qty
	if a.unit.dim != b.unit.dim {
		return false
	}
	if a.unit == b.unit {
		return a.num.shares(b.num)
	}
	return converts(*a, b.num, b.unit)
}
