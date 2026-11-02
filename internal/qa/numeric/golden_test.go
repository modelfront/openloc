package numeric

import (
	"fmt"
	"testing"
)

// For text with no date, time, measurement, web address or placeholder, the
// number findings are exactly these messages. Stored findings and the ignores
// on them are keyed by the message text, so a change here re-opens ignored
// findings: change a message only on purpose.
func TestRun_PlainNumberMessages(t *testing.T) {
	const (
		missing = "Missing number(s) in target: "
		added   = "Number(s) in target but not source: "
	)
	corpus := []struct {
		source, target, sl, tl string
		want                   []string
	}{
		{"There are 42 items and 3.14 units", "Hay 42 artículos y 3.14 unidades", "en", "es", nil},
		{"There are 42 items", "Hay artículos", "en", "es", []string{missing + "42"}},
		{"Total: 1,000 items", "Total: 1000 Artikel", "en", "de", nil},
		{"There are some items", "Hay 42 artículos", "en", "es", []string{added + "42"}},
		{"There are 42 items", "Hay 42 artículos en 7 cajas", "en", "es", []string{added + "7"}},
		{"Price: 25.16", "Preis: 2516", "en", "de", []string{missing + "25.16", added + "2516"}},
		{"Price: 25.16.", "Preis: 2516.", "en", "de", []string{missing + "25.16.", added + "2516."}},
		{"1,000 items", "1 Artikel", "en", "de", []string{missing + "1,000", added + "1"}},
		{"1.000 Stück", "1 piece", "de", "en", []string{missing + "1.000", added + "1"}},
		{"Costs 1.50 EUR", "Kostet 1,5 EUR", "en", "de", nil},
		{"Costs 1.05 EUR", "Kostet 1,5 EUR", "en", "de", []string{missing + "1.05", added + "1,5"}},
		{"Items 3, 4 and 5", "Artikel 3, 4 und 6", "en", "de", []string{missing + "5", added + "6"}},
		{"Pages 10-20", "Seiten 10–21", "en", "de", []string{missing + "20", added + "21"}},
		{"Call 911 now", "Rufen Sie jetzt an", "en", "de", []string{missing + "911"}},
		{"Free cancellation until 24 hours before check-in.", "Kostenlose Stornierung bis 48 Stunden vor der Anreise.", "en", "de", []string{missing + "24", added + "48"}},
		{"Earn 250 Nordwind Miles", "Sammeln Sie 205 Nordwind Miles", "en", "de", []string{missing + "250", added + "205"}},
		{"Room 12, floor 3", "Zimmer 12, Etage 4", "en", "de", []string{missing + "3", added + "4"}},
		{"1,000,000 users", "1.000.000 Nutzer", "en", "de", nil},
		{"1,000 piezas", "1000 pieces", "es", "en", []string{missing + "1,000", added + "1000"}},
		{"Save 30%", "Sparen Sie 35 %", "en", "de", []string{missing + "30", added + "35"}},
		{"Call +48 123 456 789", "Zadzwoń +48 123 456 789", "en", "pl", nil},
		{"Call +48 123 456 789", "Zadzwoń +48 123 456 780", "en", "pl", []string{missing + "789", added + "780"}},
	}
	for _, tc := range corpus {
		var got []string
		for _, f := range Run(tc.source, tc.target, tc.sl, tc.tl, allChecks) {
			if f.CheckType == "number_consistency" || f.CheckType == "number_added" {
				got = append(got, f.Message)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%q → %q:\n got  %q\n want %q", tc.source, tc.target, got, tc.want)
		}
	}
}
