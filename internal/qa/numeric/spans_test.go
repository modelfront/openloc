package numeric

import "testing"

func TestSpans(t *testing.T) {
	cases := []struct {
		text, lang string
		want       []string // kind:text
	}{
		{"Costs 1,234.50 on 2024-03-14 at 10:30, ships 5 km.", "en", []string{"number:1,234.50", "date:2024-03-14", "time:10:30", "measurement:5 km"}},
		{"Kostet 1.234,5 – ab 14.03.2024", "de", []string{"number:1.234,5", "date:14.03.2024"}},
		{"Résumé: 3 €", "fr", []string{"number:3"}},
		{"See https://example.com/v2 or {0} and %1$s", "en", nil},
		{"No digits here.", "en", nil},
	}
	for _, c := range cases {
		var got []string
		for _, s := range Spans(c.text, c.lang, Separators{}) {
			got = append(got, s.Kind+":"+c.text[s.Start:s.End])
		}
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v, want %v", c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %v, want %v", c.text, got, c.want)
				break
			}
		}
	}
}
