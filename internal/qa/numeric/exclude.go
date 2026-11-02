package numeric

import "openloc/internal/textspan"

// excludeSpans replaces web addresses, email addresses and software
// placeholders with spaces: their digits are not numbers a translator writes,
// so nothing in them is scanned, and offsets stay put.
func (t *text) excludeSpans() {
	matches := textspan.Any.FindAllStringIndex(t.norm, -1)
	if len(matches) == 0 {
		return
	}
	b := []byte(t.norm)
	for _, m := range matches {
		for i := m[0]; i < m[1]; i++ {
			b[i] = ' '
		}
	}
	t.norm = string(b)
}
