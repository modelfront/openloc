package numeric

import (
	"strings"
	"testing"
	"time"
)

var allChecks = Rules{Numbers: "error", Format: "warning", Dates: "warning", Times: "warning", Units: "warning"}

func find(fs []Finding, checkType string) *Finding {
	for i := range fs {
		if fs[i].CheckType == checkType {
			return &fs[i]
		}
	}
	return nil
}

type pairCase struct {
	name           string
	source, target string
	sl, tl         string
}

// Precision first: every one of these is a correct translation, and none may
// raise any finding with every check of the group on.
func TestRun_CorrectTranslationsRaiseNothing(t *testing.T) {
	cases := []pairCase{
		{"decimal comma", "Price: 25.16 EUR", "Preis: 25,16 EUR", "en", "de"},
		{"both separators", "Total 1,234.56", "Summe 1.234,56", "en", "de"},
		{"space grouping", "1,000 items", "1 000 articles", "en", "fr"},
		{"narrow no-break space grouping", "1,000 items", "1 000 articles", "en", "fr"},
		{"apostrophe grouping", "1,000 items", "1’000 Artikel", "en", "de-ch"},
		{"no grouping", "1,000 items", "1000 artículos", "en", "es"},
		{"12h to 24h", "Check-in from 3:30 PM.", "Check-in ab 15:30 Uhr.", "en", "de"},
		{"hour range", "Open 9 am to 5 pm", "Geöffnet 9 bis 17 Uhr", "en", "de"},
		{"French hours", "Check-in from 3:00 PM", "Arrivée à partir de 15 h", "en", "fr"},
		{"Japanese hour", "Starts at 3 PM", "午後3時に開始", "en", "ja"},
		{"time copied", "Arrive at 12:30.", "Ankunft um 12:30 Uhr.", "en", "de"},
		{"month name to numeric", "Offer valid until March 14, 2026.", "Angebot gültig bis 14.03.2026.", "en", "de"},
		{"month name to month name", "Offer valid until March 14, 2026.", "Offre valable jusqu'au 14 mars 2026.", "en", "fr"},
		{"Russian genitive", "Until March 14, 2026", "До 14 марта 2026 г.", "en", "ru"},
		{"US to German order", "on 03/04/2026", "am 04.03.2026", "en-us", "de"},
		{"ISO date", "Released 2026-03-14", "Veröffentlicht am 14.03.2026", "en", "de"},
		{"month and day to numeric", "Ends March 14", "Termina el 14/03", "en", "es"},
		{"month and year", "Since March 2026", "Seit März 2026", "en", "de"},
		{"Japanese date", "Until March 14, 2026", "2026年3月14日まで", "en", "ja"},
		{"feet to metres", "The pool is 82 feet long.", "Der Pool ist 25 m lang.", "en", "de"},
		{"miles to km", "Drive 5 miles", "Fahren Sie 8 km", "en", "de"},
		{"both units in source", "5 miles (8 km)", "8 km", "en", "de"},
		{"pounds to kg", "It weighs 10 lb.", "Es wiegt 4,5 kg.", "en", "de"},
		{"fahrenheit to celsius", "Heat to 70 °F.", "Auf 21 °C erhitzen.", "en", "de"},
		{"km/h to mph", "Top speed 100 km/h", "Top speed 62 mph", "de", "en"},
		{"gallons to litres", "Holds 1 gallon", "Fasst 3,8 l", "en", "de"},
		{"feet and inches", "He is 6'1\" tall", "Er ist 1,85 m groß", "en", "de"},
		{"inches to cm", "about 12 inches", "etwa 30 cm", "en", "de"},
		{"unit word not known", "Turn in 5 miles", "In 8 Kilometern abbiegen", "en", "de"},
		{"unit kept as a word", "Drive 5 miles", "Fahren Sie 5 Meilen", "en", "de"},
		{"range with en dash", "Pages 10-20", "Seiten 10–20", "en", "de"},
		{"minus sign", "Temperature: -5 °C", "Temperatur: −5 °C", "en", "de"},
		{"version", "Version 1.2.3", "Version 1.2.3", "en", "de"},
		{"web address", "See https://example.com/en/1", "Siehe https://example.com/de/2", "en", "de"},
		{"placeholder", "{0} items left", "Noch {1} Artikel", "en", "de"},
		{"printf placeholder", "Only %1$d left", "Nur noch %2$d", "en", "de"},
		{"verse", "John 3:16", "Johannes 3:16", "en", "de"},
		{"ratio", "Aspect ratio 16:9", "Seitenverhältnis 16:9", "en", "de"},
		{"fraction", "Add 3/4 cup", "3/4 Tasse hinzufügen", "en", "de"},
		{"full-width digits", "価格: ２５．１６", "Price: 25.16", "ja", "en"},
		{"percent", "Save 30%", "Sparen Sie 30 %", "en", "de"},
		{"four digits without a separator", "1000 users", "1000 Nutzer", "en", "de"},
		{"unknown language", "1,000 items", "1 Artikel", "", ""},
		{"phone number copied", "Call +48 123 456 789", "Zadzwoń +48 123 456 789", "en", "pl"},
		{"space-grouped number copied", "Population: 1 000", "Population: 1 000", "fr", "en"},
		{"year next to a number", "In 2025 150 clients signed up", "W 2025 150 klientów się zapisało", "en", "pl"},
		{"Italian set of items", "From May 1, 2 towel sets per room", "Dal 1° maggio, 2 set di asciugamani per camera", "en", "it"},
		{"euros in millions", "Revenue: €5m", "Umsatz: 5 Mio. €", "en", "de"},
		{"Romanian more", "Buy 5 more by March 2, 2026", "Cumpărați încă 5 mai mulți până la 2.III.2026", "en", "ro"},
	}
	for _, tc := range cases {
		for _, require := range []bool{false, true} {
			r := allChecks
			r.FormatRequire = require
			if got := Run(tc.source, tc.target, tc.sl, tc.tl, r); len(got) != 0 {
				t.Errorf("%s (require=%v): %q → %q: want no findings, got %+v", tc.name, require, tc.source, tc.target, got)
			}
		}
	}
}

func TestRun_RealErrorsAreFound(t *testing.T) {
	cases := []struct {
		pairCase
		checkType string
		message   string // a part of the message
	}{
		{pairCase{"dropped decimal point", "Price: 25.16", "Preis: 2516", "en", "de"}, "number_consistency", "25.16"},
		{pairCase{"range end changed", "Pages 10-20", "Seiten 10–21", "en", "de"}, "number_consistency", "20"},
		{pairCase{"dropped minus", "Temperature: -5 °C", "Temperatur: 5 °C", "en", "de"}, "number_consistency", "-5"},
		{pairCase{"time changed", "Check-in from 3:30 PM.", "Check-in ab 13:30 Uhr.", "en", "de"}, "time_consistency", "3:30 PM → 13:30 Uhr"},
		{pairCase{"date changed", "Valid until March 14, 2026.", "Gültig bis 14.04.2026.", "en", "de"}, "date_consistency", "March 14, 2026 → 14.04.2026"},
		{pairCase{"year changed", "Valid until March 14, 2026.", "Valable jusqu'au 14 mars 2025.", "en", "fr"}, "date_consistency", "14 mars 2025"},
		{pairCase{"three-letter month changed", "Arrive May 1", "Anreise am 1. Juni", "en", "de"}, "date_consistency", "May 1 → 1. Juni"},
		{pairCase{"changed to a three-letter month", "Arrive June 1", "Anreise am 1. Mai", "en", "de"}, "date_consistency", "June 1 → 1. Mai"},
		{pairCase{"Malay full name spelled like an abbreviation", "Arrive May 1", "Tiba 1 Jun", "en", "ms"}, "date_consistency", "May 1 → 1 Jun"},
		{pairCase{"Romanian May with a year", "Valid until May 14, 2026", "Valabil până la 14 mai 2025", "en", "ro"}, "date_consistency", "May 14, 2026 → 14 mai 2025"},
		{pairCase{"Korean day changed, month also alone", "Opens in December, on December 1, 2026", "12월 중 2026년 12월 2일 개장", "en", "ko"}, "date_consistency", "December 1, 2026 → 2026년 12월 2일"},
		{pairCase{"Japanese day changed, month also alone", "Opens in December, on December 1, 2026", "12月 中 2026年12月2日 開業", "en", "ja"}, "date_consistency", "December 1, 2026 → 2026年12月2日"},
		{pairCase{"unit swapped", "Drive 5 miles", "Fahren Sie 5 km", "en", "de"}, "unit_conversion", "5 miles → 5 km (about 8.0 km)"},
		{pairCase{"temperature not converted", "Heat to 82 °F.", "Auf 82 °C erhitzen.", "en", "de"}, "unit_conversion", "82 °F → 82 °C"},
		{pairCase{"wrong conversion", "Drive 5 miles", "Fahren Sie 9 km", "en", "de"}, "unit_conversion", "5 miles → 9 km"},
		{pairCase{"separator a French reader misreads", "1,000 items", "1.000 articles", "en", "fr"}, "number_format", "1.000 (expected 1 000)"},
	}
	for _, tc := range cases {
		got := find(Run(tc.source, tc.target, tc.sl, tc.tl, allChecks), tc.checkType)
		if got == nil || !strings.Contains(got.Message, tc.message) {
			t.Errorf("%s: %q → %q: want a %s finding with %q, got %+v", tc.name, tc.source, tc.target, tc.checkType, tc.message, got)
		}
	}
}

// Require accepts only the target language's separators; allow also accepts
// the source number's.
func TestRun_NumberFormatModes(t *testing.T) {
	require := allChecks
	require.FormatRequire = true
	for _, tc := range []pairCase{
		{"source format kept", "Price: 25.16", "Preis: 25.16", "en", "de"},
		{"copied, read differently", "1,000 items", "1,000 Artikel", "en", "de"},
	} {
		if f := find(Run(tc.source, tc.target, tc.sl, tc.tl, allChecks), "number_format"); f != nil {
			t.Errorf("%s, allow: want no finding, got %+v", tc.name, f)
		}
		if f := find(Run(tc.source, tc.target, tc.sl, tc.tl, require), "number_format"); f == nil {
			t.Errorf("%s, require: want a number_format finding", tc.name)
		}
	}
	f := find(Run("Price: 25.16", "Preis: 25.16", "en", "de", require), "number_format")
	if f == nil || f.Message != "Number(s) not in the target language's format: 25.16 (expected 25,16)" {
		t.Errorf("message: got %+v", f)
	}
}

// A check that is off hands its dates, times or quantities to the number
// check, which compares them as plain numbers.
func TestRun_CheckOffComparesPlainNumbers(t *testing.T) {
	numbersOnly := Rules{Numbers: "error"}
	got := Run("Check-in from 3:30 PM.", "Check-in ab 13:30 Uhr.", "en", "de", numbersOnly)
	if f := find(got, "number_consistency"); f == nil || f.Message != "Missing number(s) in target: 3" {
		t.Errorf("times off: want 3 missing, got %+v", got)
	}
	if f := find(got, "time_consistency"); f != nil {
		t.Errorf("times off: want no time finding, got %+v", f)
	}
	if got := Run("Drive 5 miles", "Fahren Sie 8 km", "en", "de", numbersOnly); find(got, "number_consistency") == nil {
		t.Errorf("units off: a conversion is a number mismatch, got %+v", got)
	}
}

// Two dates in the source and two different ones in the target cannot be
// told apart: the number check reports them, never a guessed date pair.
func TestRun_AmbiguousPairsFallBackToNumbers(t *testing.T) {
	got := Run("From March 1 to March 14, 2026", "Vom 2. bis 15. März 2026", "en", "de", allChecks)
	if find(got, "date_consistency") != nil {
		t.Errorf("want no date finding, got %+v", got)
	}
	if find(got, "number_consistency") == nil {
		t.Errorf("want the number check to report the changed days, got %+v", got)
	}
}

// A full stop that ends the sentence is not part of the date in a message.
func TestRun_DateMessageLeavesOutTheFullStop(t *testing.T) {
	f := find(Run("Valid until March 14, 2026.", "Gültig bis 14.04.2026.", "en", "de", allChecks), "date_consistency")
	if f == nil || f.Message != "Date changed in target: March 14, 2026 → 14.04.2026" {
		t.Errorf("got %+v", f)
	}
}

// Up to maxEntities quantities a side are paired, conversions included; past
// that the segment is a table, compared as plain numbers in linear time.
func TestRun_QuantitiesPastTheCapAreNumbers(t *testing.T) {
	pairs := func(n int) (string, string) {
		return strings.Repeat("10 km, ", n), strings.Repeat("6.2 mi, ", n)
	}
	src, tgt := pairs(maxEntities)
	if got := Run(src, tgt, "de", "en", allChecks); len(got) != 0 {
		t.Errorf("%d conversions: want no findings, got %+v", maxEntities, got)
	}
	src, tgt = pairs(maxEntities + 1)
	f := find(Run(src, tgt, "de", "en", allChecks), "number_consistency")
	if f == nil || f.Message != "Missing number(s) in target: 10" {
		t.Errorf("%d conversions: want them compared as plain numbers, got %+v", maxEntities+1, f)
	}
	// Pairing 40,000 against 40,000 takes about 15 s; the budget leaves a
	// wide margin for a loaded machine.
	big := strings.Repeat("5 km ", 40000)
	start := time.Now()
	if got := Run(big, big, "en", "de", allChecks); len(got) != 0 {
		t.Errorf("40,000 identical quantities: want no findings, got %+v", got)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("40,000 identical quantities took %v: they were paired one by one", d)
	}
}

// "$5m", "€5m" and "£5m" are millions, not metres.
func TestScan_CurrencyMillionsAreNotMetres(t *testing.T) {
	for _, s := range []string{"$5m", "€5m", "£5m", "¥5m"} {
		if q := scan("Revenue: "+s, localeFor("en")).qties; len(q) != 0 {
			t.Errorf("%s: read as a length %+v", s, q[0].qty)
		}
	}
	if q := scan("A 5m wall", localeFor("en")).qties; len(q) != 1 {
		t.Errorf("5m without a currency is a length, got %d quantities", len(q))
	}
}

// A target of many plain numbers against the most quantities that are still
// paired, none of which they stand for: the quantities fall back to the
// number check.
func TestRun_PlainNumbersAgainstQuantities(t *testing.T) {
	src := strings.Repeat("5 km ", maxEntities)
	got := Run(src, strings.Repeat("1 ", 5000), "en", "de", allChecks)
	if f := find(got, "number_consistency"); f == nil || f.Message != "Missing number(s) in target: 5" {
		t.Errorf("want 5 missing, got %+v", got)
	}
}

// Taking a quantity from a large pool of plain numbers costs a lookup, not a
// search: searching 500,000 numbers for every quantity takes seconds.
func TestPlainPool_TakeIsALookup(t *testing.T) {
	sd := scan(strings.Repeat("1 ", 500000), localeFor("de"))
	pool := newPlainPool(sd.numbers)
	q := scan("5 km", localeFor("en")).qties[0].qty
	start := time.Now()
	for range maxEntities {
		if pool.take(*q) {
			t.Fatal("5 km taken by a 1")
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v: each take searched every number", d)
	}
}

// A plain number stands for one quantity: "5 Meilen" covers one of two
// "5 miles", the other is missing.
func TestRun_PlainNumberStandsForOneQuantity(t *testing.T) {
	got := Run("Drive 5 miles, then 5 miles more", "Fahren Sie 5 Meilen", "en", "de", allChecks)
	if f := find(got, "number_consistency"); f == nil || f.Message != "Missing number(s) in target: 5" {
		t.Errorf("want one 5 missing, got %+v", got)
	}
}

// A plain number with the quantity's own value is found however many numbers
// the other side has; a converted one is searched for only up to
// convertBudget numbers.
func TestRun_PlainNumbersPastTheConversionBudget(t *testing.T) {
	ones := func(n int) string { return strings.Repeat(" 1", n) }
	if f := find(Run("Drive 5 miles", "Fahren Sie 5 Meilen"+ones(convertBudget+500), "en", "de", allChecks), "number_consistency"); f != nil {
		t.Errorf("same value past the budget: want no missing number, got %+v", f)
	}
	if f := find(Run("Turn in 5 miles", "In 8 Kilometern abbiegen"+ones(convertBudget-10), "en", "de", allChecks), "number_consistency"); f != nil {
		t.Errorf("conversion within the budget: want no missing number, got %+v", f)
	}
	f := find(Run("Turn in 5 miles", "In 8 Kilometern abbiegen"+ones(convertBudget+10), "en", "de", allChecks), "number_consistency")
	if f == nil || f.Message != "Missing number(s) in target: 5" {
		t.Errorf("conversion past the budget: want 5 missing, got %+v", f)
	}
}

// Month patterns are cached per list of month names: two languages that name
// only some months alike each get their own.
func TestMonthPatterns_CachedPerMonthList(t *testing.T) {
	a := localeFor("en")
	b := a
	b.months[8] = []string{"Septembrino"}
	if p := monthPatternsFor(a); p.byName["Septembrino"] != 0 {
		t.Fatal("precondition: en has no Septembrino")
	}
	if p := monthPatternsFor(b); p.byName["Septembrino"] != 9 {
		t.Errorf("a language that differs only in September got another's patterns")
	}
}

// A language's separators can be set by the profile: they decide how a
// number such as "1,000" is read on either side and which separators the
// format check expects. Without them the language's CLDR data decides.
func TestRun_SeparatorOverrides(t *testing.T) {
	english := Separators{Decimal: '.', Group: ','}
	withTarget := func(r Rules, s Separators) Rules { r.TargetSeparators = s; return r }
	withSource := func(r Rules, s Separators) Rules { r.SourceSeparators = s; return r }
	numbersOnly := Rules{Numbers: "error"}

	// Spanish reads "1,000" as one; written the English way it is a thousand.
	if find(Run("1.000 Stück", "1,000 piezas", "de", "es", numbersOnly), "number_consistency") == nil {
		t.Error("es default: 1,000 is one, a source of 1.000 (de) must be flagged")
	}
	if got := Run("1.000 Stück", "1,000 piezas", "de", "es", withTarget(numbersOnly, english)); len(got) != 0 {
		t.Errorf("es written the English way: 1,000 is a thousand, got %+v", got)
	}
	if got := Run("1,000 piezas", "1000 pieces", "es", "en", withSource(numbersOnly, english)); len(got) != 0 {
		t.Errorf("source es written the English way: 1,000 is a thousand, got %+v", got)
	}

	require := allChecks
	require.FormatRequire = true
	if find(Run("Price: 25.16", "Precio: 25.16", "en", "es", require), "number_format") == nil {
		t.Error("es default, require: 25.16 must be flagged")
	}
	if got := find(Run("Price: 25.16", "Precio: 25.16", "en", "es", withTarget(require, english)), "number_format"); got != nil {
		t.Errorf("es written the English way, require: 25.16 is right, got %+v", got)
	}
	f := find(Run("Total 1234.5", "Total 1234,5", "en", "es", withTarget(require, english)), "number_format")
	if f == nil || f.Message != "Number(s) not in the target language's format: 1234,5 (expected 1,234.5)" {
		t.Errorf("es written the English way, require: want 1234,5 flagged with 1,234.5 expected, got %+v", f)
	}

	// A space group is read as grouping, and suggested with a no-break space.
	swissFrench := Separators{Decimal: ',', Group: ' '}
	if got := Run("1,500 items", "1 500 articles", "en", "it", withTarget(numbersOnly, swissFrench)); len(got) != 0 {
		t.Errorf("space group: 1 500 is 1500, got %+v", got)
	}
	f = find(Run("Total 1234.5", "Totale 1234.5", "en", "it", withTarget(require, swissFrench)), "number_format")
	if f == nil || f.Message != "Number(s) not in the target language's format: 1234.5 (expected 1 234,5)" {
		t.Errorf("space group suggestion: got %+v", f)
	}

	// No grouping: a grouped number is flagged and suggested without groups.
	f = find(Run("Total 1,234.5", "Total 1,234.5", "en", "es", withTarget(require, Separators{Decimal: '.'})), "number_format")
	if f == nil || f.Message != "Number(s) not in the target language's format: 1,234.5 (expected 1234.5)" {
		t.Errorf("no grouping: got %+v", f)
	}

	// A language CLDR does not know is graded once the profile names its separators.
	if find(Run("Price: 25.16", "Price: 25,16", "en", "xx", withTarget(require, english)), "number_format") == nil {
		t.Error("unknown language with separators set: 25,16 must be flagged")
	}
}
