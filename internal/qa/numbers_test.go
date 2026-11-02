package qa

import (
	"fmt"
	"testing"

	"openloc/internal/qa/numeric"
)

// Languages are a segment's source and target language codes.
type Languages struct {
	Source, Target string
}

// CheckNumbers runs the number check alone, as a profile with only Number
// consistency on does.
func CheckNumbers(source, target string, langs Languages, severity string) []Issue {
	return issuesOf(numeric.Run(source, target, langs.Source, langs.Target, numeric.Rules{Numbers: severity}))
}

// findIssue returns the first issue with the given check_type, or nil.
func findIssue(issues []Issue, checkType string) *Issue {
	for i := range issues {
		if issues[i].CheckType == checkType {
			return &issues[i]
		}
	}
	return nil
}

func TestCheckNumbers_AllPresent(t *testing.T) {
	issues := CheckNumbers("There are 42 items and 3.14 units", "Hay 42 artículos y 3.14 unidades", Languages{}, "error")
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %+v", issues)
	}
}

func TestCheckNumbers_MissingNumber(t *testing.T) {
	issues := CheckNumbers("There are 42 items", "Hay artículos", Languages{}, "error")
	miss := findIssue(issues, "number_consistency")
	if miss == nil {
		t.Fatal("expected number_consistency issue for missing number")
	}
	// Dropping a source number keeps the rule's configured (blocking) severity.
	if miss.Severity != "error" {
		t.Errorf("missing-number issue should carry the configured severity 'error', got %q", miss.Severity)
	}
}

func TestCheckNumbers_ThousandSeparator(t *testing.T) {
	issues := CheckNumbers("Total: 1,000 items", "Total: 1000 Artikel", Languages{}, "error")
	if len(issues) != 0 {
		t.Fatalf("expected no issues (1,000 = 1000), got: %+v", issues)
	}
}

func TestCheckNumbers_NoNumbers(t *testing.T) {
	issues := CheckNumbers("Hello world", "Hola mundo", Languages{}, "error")
	if len(issues) != 0 {
		t.Fatal("expected no issues when neither side has numbers")
	}
}

// A number present ONLY in the translation (not the source)
// must be flagged — but as a non-blocking WARNING (number_added), not the
// blocking error, even when the source has no numbers at all.
func TestCheckNumbers_ExtraInTargetOnly(t *testing.T) {
	issues := CheckNumbers("There are some items", "Hay 42 artículos", Languages{}, "error")
	added := findIssue(issues, "number_added")
	if added == nil {
		t.Fatal("expected number_added issue: 42 appears in target but not source")
	}
	if added.Severity != "warning" {
		t.Errorf("target-only number must be a non-blocking warning, got severity %q", added.Severity)
	}
	// It must NOT surface as the blocking number_consistency error.
	if findIssue(issues, "number_consistency") != nil {
		t.Error("a target-only number must not raise the blocking number_consistency error")
	}
}

func TestCheckNumbers_ExtraWhenSourceAlsoHasNumbers(t *testing.T) {
	issues := CheckNumbers("There are 42 items", "Hay 42 artículos en 7 cajas", Languages{}, "error")
	added := findIssue(issues, "number_added")
	if added == nil || added.Severity != "warning" {
		t.Fatalf("expected a number_added warning for the target-only 7, got: %+v", issues)
	}
}

var enToDE = Languages{Source: "en", Target: "de"}

// A separator is part of the number's value: a dropped or moved decimal point
// is a different number.
func TestCheckNumbers_DroppedDecimalPoint(t *testing.T) {
	for _, langs := range []Languages{enToDE, {Source: "en", Target: "en-gb"}, {}} {
		issues := CheckNumbers("Price: 25.16", "Preis: 2516", langs, "error")
		miss := findIssue(issues, "number_consistency")
		if miss == nil || miss.Message != "Missing number(s) in target: 25.16" {
			t.Errorf("%+v: expected 25.16 reported missing, got %+v", langs, issues)
		}
		if findIssue(issues, "number_added") == nil {
			t.Errorf("%+v: expected 2516 reported as added, got %+v", langs, issues)
		}
	}
	if findIssue(CheckNumbers("Price: 25.16.", "Preis: 2516.", enToDE, "error"), "number_consistency") == nil {
		t.Error("a full stop after the number must not turn 25.16 into a grouped 2516")
	}
	if findIssue(CheckNumbers("1.5 kg", "15 kg", Languages{}, "error"), "number_consistency") == nil {
		t.Error("1.5 and 15 must not count as the same number")
	}
	if findIssue(CheckNumbers("2,5 kg", "25 kg", Languages{Source: "de", Target: "en"}, "error"), "number_consistency") == nil {
		t.Error("de 2,5 and en 25 must not count as the same number")
	}
}

// The same value written the target language's way matches.
func TestCheckNumbers_LocalizedFormatMatches(t *testing.T) {
	tests := []struct {
		name           string
		source, target string
		langs          Languages
	}{
		{"decimal comma", "Price: 25.16", "Preis: 25,16", enToDE},
		{"both separators", "Total 1,234.56", "Summe 1.234,56", enToDE},
		{"grouping only", "1,000 items", "1.000 Artikel", enToDE},
		{"several groups", "1,000,000 users", "1.000.000 Nutzer", enToDE},
		{"no grouping", "1,000 items", "1000 Artikel", enToDE},
		{"zero before the point", "0.500 mm", "0,500 mm", enToDE},
		{"copied unchanged", "1,000 items", "1,000 Artikel", enToDE},
		{"de to en", "2,5 kg und 1.000 Stück", "2.5 kg and 1,000 pieces", Languages{Source: "de", Target: "en"}},
		{"sentence end", "It costs 25.16.", "Es kostet 25,16.", enToDE},
		{"list", "Items 3, 4 and 5", "Artikel 3, 4 und 5", enToDE},
		{"version number", "Version 1.2.3", "Version 1.2.3", enToDE},
	}
	for _, tt := range tests {
		if issues := CheckNumbers(tt.source, tt.target, tt.langs, "error"); len(issues) != 0 {
			t.Errorf("%s: %q → %q: expected no issues, got %+v", tt.name, tt.source, tt.target, issues)
		}
	}
}

// "1,000" is one thousand in English and one in German. The language decides,
// and with no language known either reading is accepted.
func TestCheckNumbers_AmbiguousSeparatorFollowsLanguage(t *testing.T) {
	if findIssue(CheckNumbers("1,000 items", "1 Artikel", enToDE, "error"), "number_consistency") == nil {
		t.Error("en 1,000 is one thousand: a target of 1 must be flagged")
	}
	if findIssue(CheckNumbers("1.000 Stück", "1 piece", Languages{Source: "de", Target: "en"}, "error"), "number_consistency") == nil {
		t.Error("de 1.000 is one thousand: a target of 1 must be flagged")
	}
	// The region decides too: Mexican Spanish writes "1,000" for one thousand,
	// Spanish for one.
	if issues := CheckNumbers("1,000 piezas", "1000 pieces", Languages{Source: "es-mx", Target: "en"}, "error"); len(issues) != 0 {
		t.Errorf("es-mx 1,000 is one thousand: expected no issues, got %+v", issues)
	}
	if findIssue(CheckNumbers("1,000 piezas", "1000 pieces", Languages{Source: "es", Target: "en"}, "error"), "number_consistency") == nil {
		t.Error("es 1,000 is one: a target of 1000 must be flagged")
	}
	if issues := CheckNumbers("1,000 items", "1 Artikel", Languages{}, "error"); len(issues) != 0 {
		t.Errorf("with no language, 1,000 may read as 1: expected no issues, got %+v", issues)
	}
}

func TestCheckNumbers_TrailingZerosAfterThePoint(t *testing.T) {
	if issues := CheckNumbers("Costs 1.50 EUR", "Kostet 1,5 EUR", enToDE, "error"); len(issues) != 0 {
		t.Errorf("1.50 and 1,5 are the same value: expected no issues, got %+v", issues)
	}
	if findIssue(CheckNumbers("Costs 1.05 EUR", "Kostet 1,5 EUR", enToDE, "error"), "number_consistency") == nil {
		t.Error("1.05 and 1,5 differ")
	}
}

// A dash between two numbers is a range, not a minus sign: the target may
// write the range with an en dash.
func TestCheckNumbers_RangeIsNotANegativeNumber(t *testing.T) {
	if issues := CheckNumbers("Pages 10-20", "Seiten 10–20", enToDE, "error"); len(issues) != 0 {
		t.Errorf("expected no issues for a range, got %+v", issues)
	}
	if issues := CheckNumbers("Temperature: -5 °C", "Temperatur: −5 °C", enToDE, "error"); len(issues) != 0 {
		t.Errorf("a minus sign (U+2212) is a minus: expected no issues, got %+v", issues)
	}
	if findIssue(CheckNumbers("Temperature: -5 °C", "Temperatur: 5 °C", enToDE, "error"), "number_consistency") == nil {
		t.Error("a dropped minus sign must be flagged")
	}
}

// A number_format config is stored with its mode filled in and its language
// codes normalized; anything the UI cannot show is refused.
func TestValidateNumberFormatConfig(t *testing.T) {
	ok := []struct{ in, want string }{
		{`{}`, `{"mode":"allow"}`},
		{`{"mode":"require","languages":[{"language":"ES_mx","decimal":".","group":","}]}`,
			`{"mode":"require","languages":[{"language":"es-mx","decimal":".","group":","}]}`},
		{`{"mode":"allow","languages":[{"language":"fr","decimal":",","group":" "},{"language":"de-ch","decimal":".","group":"'"},{"language":"ja","decimal":".","group":""}]}`,
			`{"mode":"allow","languages":[{"language":"fr","decimal":",","group":" "},{"language":"de-ch","decimal":".","group":"'"},{"language":"ja","decimal":".","group":""}]}`},
	}
	for _, c := range ok {
		got, err := ValidateNumberFormatConfig([]byte(c.in))
		if err != nil || string(got) != c.want {
			t.Errorf("%s: got %s, %v; want %s", c.in, got, err, c.want)
		}
	}
	tooMany := `{"languages":[`
	for i := range MaxNumberFormatLanguages + 1 {
		if i > 0 {
			tooMany += ","
		}
		tooMany += fmt.Sprintf(`{"language":"x%d","decimal":".","group":","}`, i)
	}
	tooMany += `]}`
	for _, in := range []string{
		`{"mode":"strict"}`,
		`{"mode":"allow","langs":[]}`,
		`{"languages":[{"language":"","decimal":".","group":","}]}`,
		`{"languages":[{"language":"es","decimal":"x","group":","}]}`,
		`{"languages":[{"language":"es","decimal":"","group":","}]}`,
		`{"languages":[{"language":"es","decimal":".","group":"_"}]}`,
		`{"languages":[{"language":"es","decimal":".","group":"."}]}`,
		`{"languages":[{"language":"es","decimal":".","group":","},{"language":"ES","decimal":",","group":"."}]}`,
		tooMany,
	} {
		if _, err := ValidateNumberFormatConfig([]byte(in)); err == nil {
			t.Errorf("%.80s: want an error", in)
		}
	}
}

// A locale's own entry wins over its bare language's, which covers every
// other locale of the language.
func TestNumberFormatConfig_SeparatorsFor(t *testing.T) {
	cfg, err := ParseNumberFormatConfig([]byte(`{"languages":[{"language":"es","decimal":".","group":","},{"language":"es-mx","decimal":",","group":" "}]}`))
	if err != nil {
		t.Fatal(err)
	}
	english, spaced := numeric.Separators{Decimal: '.', Group: ','}, numeric.Separators{Decimal: ',', Group: ' '}
	for code, want := range map[string]numeric.Separators{
		"es": english, "ES": english, "es-ar": english, "es_AR": english,
		"es-mx": spaced, "es-MX": spaced,
		"fr": {}, "": {},
	} {
		if got := cfg.SeparatorsFor(code); got != want {
			t.Errorf("%q: got %+v, want %+v", code, got, want)
		}
	}
}

// The language separators reach the number checks through CheckAllWithParams,
// for source and target alike, while number_format is on; with it off they are
// ignored.
func TestCheckAllWithParams_NumberFormatLanguages(t *testing.T) {
	cfg, err := ParseNumberFormatConfig([]byte(`{"languages":[{"language":"es","decimal":".","group":","}]}`))
	if err != nil {
		t.Fatal(err)
	}
	formatOn := map[string]string{"number_consistency": "error", "number_format": "warning"}
	p := CheckAllParams{Source: "1.000 Stück", Target: "1,000 piezas", Rules: formatOn,
		SourceLanguage: "de", TargetLanguage: "es-ES"}
	if findIssue(CheckAllWithParams(t.Context(), p), "number_consistency") == nil {
		t.Fatal("without the override, es reads 1,000 as one: want a finding")
	}
	p.NumberFormat = cfg
	if issues := CheckAllWithParams(t.Context(), p); len(issues) != 0 {
		t.Errorf("es target written the English way: want no finding, got %+v", issues)
	}
	p.Rules = map[string]string{"number_consistency": "error"}
	if findIssue(CheckAllWithParams(t.Context(), p), "number_consistency") == nil {
		t.Error("number_format off: the override must be ignored, so es reads 1,000 as one")
	}

	src := CheckAllParams{Source: "1,000 piezas", Target: "1000 pieces", Rules: formatOn,
		SourceLanguage: "es", TargetLanguage: "en"}
	if findIssue(CheckAllWithParams(t.Context(), src), "number_consistency") == nil {
		t.Fatal("without the override, an es source reads 1,000 as one: want a finding")
	}
	src.NumberFormat = cfg
	if issues := CheckAllWithParams(t.Context(), src); len(issues) != 0 {
		t.Errorf("es source written the English way: want no finding, got %+v", issues)
	}
}
