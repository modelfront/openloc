package segmentation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readSRX(t *testing.T, name string) SRXImport {
	t.Helper()
	raw, err := os.ReadFile("testdata/srx/" + name)
	if err != nil {
		t.Fatal(err)
	}
	imp, err := ParseSRX(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return imp
}

func draftNamed(t *testing.T, imp SRXImport, name string) SRXDraft {
	t.Helper()
	for _, d := range imp.Drafts {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no draft %q in %+v", name, imp.Drafts)
	return SRXDraft{}
}

func TestParseSRX_CascadeFlattensLaterMaps(t *testing.T) {
	imp := readSRX(t, "cascade.srx")
	if len(imp.Drafts) != 3 {
		t.Fatalf("drafts = %d, want one per language rule", len(imp.Drafts))
	}
	en := draftNamed(t, imp, "English")
	want := []Rule{
		{Before: `\bMr\.`, After: `\s`},
		{Before: `[\.\?!]+`, After: `\s+A`, Break: true},
		{Before: `\n`, After: ``, Break: true},
	}
	if !reflect.DeepEqual(en.RuleSet.Rules, want) {
		t.Errorf("English rules = %+v\nwant own rules then Default's %+v", en.RuleSet.Rules, want)
	}
	if !reflect.DeepEqual(en.SourceLanguages, []string{"en"}) {
		t.Errorf("English languages = %q, want en", en.SourceLanguages)
	}
	fr := draftNamed(t, imp, "French")
	if !reflect.DeepEqual(fr.SourceLanguages, []string{"fr-ca"}) || len(fr.RuleSet.Rules) != 3 {
		t.Errorf("French = %q with %d rules, want fr-ca with its rule and Default's two", fr.SourceLanguages, len(fr.RuleSet.Rules))
	}
	def := draftNamed(t, imp, "Default")
	if len(def.SourceLanguages) != 0 || len(def.RuleSet.Rules) != 2 {
		t.Errorf("Default = %q with %d rules, want no language and its own two", def.SourceLanguages, len(def.RuleSet.Rules))
	}
	if !strings.Contains(strings.Join(imp.Warnings, "\n"), `segmentsubflows="no" is ignored`) ||
		!strings.Contains(strings.Join(imp.Warnings, "\n"), `formathandle type="end" include="no" is ignored`) {
		t.Errorf("file warnings = %q, want the ignored header settings", imp.Warnings)
	}
}

func TestParseSRX_UnsupportedPatternIsReportedNotDropped(t *testing.T) {
	en := draftNamed(t, readSRX(t, "cascade.srx"), "English")
	joined := strings.Join(en.Warnings, "\n")
	if !strings.Contains(joined, `Rule 2 was left out: the before pattern "(?<=\s)Dr\.": look-behind is not supported.`) {
		t.Errorf("warnings = %q, want rule 2 named with its reason", en.Warnings)
	}
	for _, r := range en.RuleSet.Rules {
		if strings.Contains(r.Before, "Dr") {
			t.Errorf("unsupported rule kept: %+v", r)
		}
	}
}

// manySRX builds a cascading SRX file with n language rules of k rules each,
// every map matching every language.
func manySRX(n, k int) []byte {
	var b strings.Builder
	b.WriteString(`<srx version="2.0"><header cascade="yes"/><body><languagerules>`)
	for i := range n {
		fmt.Fprintf(&b, `<languagerule languagerulename="L%d">`, i)
		for j := range k {
			fmt.Fprintf(&b, `<rule break="yes"><beforebreak>x%d</beforebreak><afterbreak>\s</afterbreak></rule>`, j)
		}
		b.WriteString(`</languagerule>`)
	}
	b.WriteString(`</languagerules><maprules>`)
	for i := range n {
		fmt.Fprintf(&b, `<languagemap languagepattern="en.*" languagerulename="L%d"/>`, i)
	}
	b.WriteString(`</maprules></body></srx>`)
	return []byte(b.String())
}

// A file at the language rule cap, every rule cascading into every later
// one, still gives drafts within the rule limit.
func TestParseSRX_CascadeStaysWithinLimits(t *testing.T) {
	imp, err := ParseSRX(manySRX(MaxSRXLanguageRules, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Drafts) != MaxSRXLanguageRules {
		t.Fatalf("drafts = %d, want %d", len(imp.Drafts), MaxSRXLanguageRules)
	}
	first := imp.Drafts[0]
	if len(first.RuleSet.Rules) != MaxRules || !strings.Contains(strings.Join(first.Warnings, "\n"), fmt.Sprintf("Only the first %d of", MaxRules)) {
		t.Errorf("first draft has %d rules and warnings %q, want %d and the cut named", len(first.RuleSet.Rules), first.Warnings, MaxRules)
	}
	if last := imp.Drafts[len(imp.Drafts)-1]; len(last.RuleSet.Rules) != 10 {
		t.Errorf("last draft has %d rules, want only its own 10", len(last.RuleSet.Rules))
	}
	if _, err := ParseSRX(manySRX(MaxSRXLanguageRules+1, 1)); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d language rules", MaxSRXLanguageRules)) {
		t.Errorf("one language rule over the cap: err = %v, want it rejected", err)
	}
}

// Many maps naming a language rule whose rules are all left out: a draft
// takes that rule once, and its warnings stop at MaxSRXWarnings.
func TestParseSRX_RepeatedMapsAndEmptyRulesStayBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<srx version="2.0"><header cascade="yes"/><body><languagerules>`)
	b.WriteString(`<languagerule languagerulename="A"><rule break="yes"><beforebreak>\.</beforebreak><afterbreak>\s</afterbreak></rule></languagerule>`)
	b.WriteString(`<languagerule languagerulename="B">` + strings.Repeat(`<rule/>`, 5000) + `</languagerule>`)
	b.WriteString(`</languagerules><maprules><languagemap languagepattern="en.*" languagerulename="A"/>`)
	b.WriteString(strings.Repeat(`<languagemap languagepattern=".*" languagerulename="B"/>`, MaxSRXLanguageMaps-1))
	b.WriteString(`</maprules></body></srx>`)
	imp, err := ParseSRX([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	a := draftNamed(t, imp, "A")
	if len(a.RuleSet.Rules) != 1 {
		t.Errorf("A has %d rules, want its own one", len(a.RuleSet.Rules))
	}
	if len(a.Warnings) != MaxSRXWarnings+1 || a.Warnings[MaxSRXWarnings] != fmt.Sprintf("%d more problems are not listed.", 5000-MaxSRXWarnings) {
		t.Errorf("A has %d warnings ending %q, want %d and a count of the rest", len(a.Warnings), a.Warnings[len(a.Warnings)-1], MaxSRXWarnings+1)
	}

	// More than MaxRules rules after more than MaxSRXWarnings problems: the
	// cut is still named.
	var c strings.Builder
	c.WriteString(`<srx version="2.0"><header cascade="no"/><body><languagerules><languagerule languagerulename="C">`)
	c.WriteString(strings.Repeat(`<rule/>`, MaxSRXWarnings+50))
	c.WriteString(strings.Repeat(`<rule break="yes"><beforebreak>\.</beforebreak><afterbreak>\s</afterbreak></rule>`, MaxRules+1))
	c.WriteString(`</languagerule></languagerules><maprules><languagemap languagepattern="en.*" languagerulename="C"/></maprules></body></srx>`)
	cut, err := ParseSRX([]byte(c.String()))
	if err != nil {
		t.Fatal(err)
	}
	if w := draftNamed(t, cut, "C").Warnings; len(w) == 0 || w[0] != fmt.Sprintf("Only the first %d of %d rules were kept.", MaxRules, MaxRules+1) {
		t.Errorf("warnings start %q, want the rule cut first", w[:min(2, len(w))])
	}

	tooMany := strings.Replace(b.String(), `</maprules>`, `<languagemap languagepattern="x" languagerulename="B"/></maprules>`, 1)
	if _, err := ParseSRX([]byte(tooMany)); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d language maps", MaxSRXLanguageMaps)) {
		t.Errorf("one map over the cap: err = %v, want it rejected", err)
	}
}

// A pattern over MaxPatternSize is left out and named, like one RE2 cannot run.
func TestParseSRX_TooComplexPatternIsReported(t *testing.T) {
	imp, err := ParseSRX([]byte(`<srx version="2.0"><header/><body><languagerules><languagerule languagerulename="Big"><rule break="yes"><beforebreak>(?s).{0,999}</beforebreak><afterbreak>\s</afterbreak></rule></languagerule></languagerules><maprules><languagemap languagepattern="en.*" languagerulename="Big"/></maprules></body></srx>`))
	if err != nil {
		t.Fatal(err)
	}
	big := draftNamed(t, imp, "Big")
	if len(big.RuleSet.Rules) != 0 || len(big.Warnings) != 1 || !strings.Contains(big.Warnings[0], "too complex") {
		t.Errorf("draft = %+v, want the rule left out as too complex", big)
	}
}

func TestParseSRX_NoCascadeKeepsOwnRules(t *testing.T) {
	imp := readSRX(t, "nocascade.srx")
	// A draft without problems still lists them as an empty array.
	clean, err := ParseSRX([]byte(`<srx version="2.0"><header/><body><languagerules><languagerule languagerulename="Clean"><rule break="yes"><beforebreak>\.</beforebreak><afterbreak>\s</afterbreak></rule></languagerule></languagerules><maprules><languagemap languagepattern="en.*" languagerulename="Clean"/></maprules></body></srx>`))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := json.Marshal(draftNamed(t, clean, "Clean")); err != nil || !strings.Contains(string(raw), `"warnings":[]`) {
		t.Errorf("draft JSON = %s (%v), want an empty warnings array", raw, err)
	}
	if en := draftNamed(t, imp, "English"); len(en.RuleSet.Rules) != 1 {
		t.Errorf("English rules = %+v, want only its own", en.RuleSet.Rules)
	}
	if len(imp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none for default header settings", imp.Warnings)
	}
}

func TestParseSRX_Version1(t *testing.T) {
	de := draftNamed(t, readSRX(t, "srx10.srx"), "German")
	if !reflect.DeepEqual(de.SourceLanguages, []string{"de"}) || len(de.RuleSet.Rules) != 2 || de.RuleSet.Rules[0].Break || !de.RuleSet.Rules[1].Break {
		t.Errorf("German = %+v", de)
	}
	if _, err := Compile(de.RuleSet); err != nil {
		t.Errorf("imported set does not compile: %v", err)
	}
}

func TestParseSRX_Rejects(t *testing.T) {
	malformed, err := os.ReadFile("testdata/srx/malformed.srx")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"malformed XML":  malformed,
		"empty":          nil,
		"no rules":       []byte(`<srx version="2.0"><header/><body><languagerules/></body></srx>`),
		"not SRX":        []byte(`<xliff version="1.2"/>`),
		"over the cap":   append([]byte("<srx>"), make([]byte, MaxSRXSize)...),
		"entity outside": []byte(`<!DOCTYPE srx [<!ENTITY x SYSTEM "file:///etc/passwd">]><srx><body><languagerules><languagerule languagerulename="&x;"/></languagerules></body></srx>`),
	}
	for name, data := range cases {
		_, err := ParseSRX(data)
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
}

func TestLanguageFromPattern(t *testing.T) {
	tests := map[string]string{
		"EN.*": "en", "en": "en", "(?i)de.*": "de", "fr_CA": "fr-ca", "FR-CA.*": "fr-ca",
		"(?i)pt[-_]br(?:[-_].*)?": "pt-br", "(?i)ja(?:[-_].*)?": "ja",
		".*": "", "(EN|DE).*": "", "XX.*": "", "": "",
	}
	for pattern, want := range tests {
		if got := languageFromPattern(pattern); got != want {
			t.Errorf("languageFromPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestICUToRE2(t *testing.T) {
	for in, want := range map[string]string{
		`\u00A0`: `\x{A0}`, `[\u3002\uFF01]`: `[\x{3002}\x{FF01}]`, `\U0001F600`: `\x{1F600}`,
		`\\u0041`: `\\u0041`, `\u0000`: `\x{0}`, `plain`: `plain`,
	} {
		if got := icuToRE2(in); got != want {
			t.Errorf("icuToRE2(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSRX_RoundTrip(t *testing.T) {
	sets := map[string]struct {
		langs []string
		rs    RuleSet
	}{
		"Paragraph with rules & <marks>": {[]string{"th"}, RuleSet{Mode: ModeParagraph, Rules: []Rule{
			{Before: `[.]`, After: `\s`, Break: true}, {Before: `x`, After: `y`},
		}, Abbreviations: []string{"a&b"}}},
		"Any language, empty": {[]string{}, RuleSet{}},
		"Two languages":       {[]string{"de", "fr-ca"}, RuleSet{Rules: []Rule{{Before: `\.`, After: `\s`, Break: true}}}},
		"Regional \"quoted\"": {[]string{"fr-ca"}, RuleSet{Rules: []Rule{{Before: `<`, After: `>`, Break: true}}, NumberAbbreviations: []string{"No", "n°"}}},
	}
	for _, b := range Builtins() {
		langs := []string{}
		if b.SourceLanguage != "" {
			langs = []string{b.SourceLanguage}
		}
		sets[b.Name] = struct {
			langs []string
			rs    RuleSet
		}{langs, b.RuleSet}
	}
	for name, s := range sets {
		raw, err := ExportSRX(name, s.langs, s.rs)
		if err != nil {
			t.Fatalf("%s: export: %v", name, err)
		}
		imp, err := ParseSRX(raw)
		if err != nil {
			t.Fatalf("%s: import: %v\n%s", name, err, raw)
		}
		if len(imp.Drafts) != 1 || len(imp.Warnings) != 0 {
			t.Fatalf("%s: import = %+v", name, imp)
		}
		d := imp.Drafts[0]
		if d.Name != name || !reflect.DeepEqual(d.SourceLanguages, s.langs) || len(d.Warnings) != 0 {
			t.Errorf("%s: draft %q in %q with warnings %q", name, d.Name, d.SourceLanguages, d.Warnings)
		}
		if want := Normalize(s.rs); !reflect.DeepEqual(d.RuleSet, want) {
			t.Errorf("%s: round trip changed the rules\n got %+v\nwant %+v", name, d.RuleSet, want)
		}
	}
}

// Another tool reads the word lists as no-break rules: the exported file,
// read without the extension element, segments like the set.
func TestExportSRX_WordListsAsRulesForOtherTools(t *testing.T) {
	rs := RuleSet{Rules: []Rule{{Before: `\.`, After: `\s`, Break: true}}, Abbreviations: []string{"Dr"}, NumberAbbreviations: []string{"No"}}
	raw, err := ExportSRX("x", []string{"en"}, rs)
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.Replace(string(raw), `xmlns="urn:openloc:srx:wordlists:1"`, `xmlns="urn:other"`, 1)
	imp, err := ParseSRX([]byte(stripped))
	if err != nil {
		t.Fatal(err)
	}
	other := MustCompile(imp.Drafts[0].RuleSet)
	text := "Ask Dr. Who about No. 5 now. Then go."
	got := segments(t, other, text, nil)
	if want := segments(t, MustCompile(rs), text, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("rules-only reading = %q, the set = %q", got, want)
	}
}
