package langcode

// BestMatch picks the item whose language covers target: an exact match on
// the normalized code first, else the first item sharing target's base
// language (a rule for "fr" covers an "fr-ca" job and vice versa). Items are
// scanned in order, so callers pass them pre-sorted by priority. Nil when
// nothing matches or target is empty.
func BestMatch[T any](items []T, lang func(T) string, target string) *T {
	want := Normalize(target)
	if want == "" {
		return nil
	}
	var base *T
	for i := range items {
		have := Normalize(lang(items[i]))
		if have == want {
			return &items[i]
		}
		if base == nil && SameBase(have, want) {
			base = &items[i]
		}
	}
	return base
}
