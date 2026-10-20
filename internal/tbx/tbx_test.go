package tbx

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func TestParseConcepts_RejectsNonTBX(t *testing.T) {
	src := `<?xml version="1.0"?><resources><string name="x">y</string></resources>`
	_, _, err := ParseConcepts(bytes.NewReader([]byte(src)))
	var unsup *UnsupportedError
	if !errors.As(err, &unsup) {
		t.Fatalf("expected UnsupportedError, got %v", err)
	}
	if unsup.Root != "resources" {
		t.Errorf("expected root=%q, got %q", "resources", unsup.Root)
	}
}

func TestParseConcepts_RejectsDCT(t *testing.T) {
	src := `<?xml version="1.0"?><tbx style="dct" type="TBX-Basic" xmlns="urn:iso:std:iso:30042:ed-2"><text><body></body></text></tbx>`
	_, _, err := ParseConcepts(bytes.NewReader([]byte(src)))
	var unsup *UnsupportedError
	if !errors.As(err, &unsup) {
		t.Fatalf("expected UnsupportedError, got %v", err)
	}
	if unsup.Style != "dct" || unsup.Type != "TBX-Basic" {
		t.Errorf("expected style=dct type=TBX-Basic; got style=%q type=%q", unsup.Style, unsup.Type)
	}
}

// A supported DCA file (no style attr defaults through) must NOT be rejected.
func TestParseConcepts_AcceptsDCA(t *testing.T) {
	src := `<?xml version="1.0"?><tbx style="dca" type="TBX-Basic" xmlns="urn:iso:std:iso:30042:ed-2"><text><body>
	  <conceptEntry id="c1"><langSec xml:lang="en"><termSec><term>hi</term></termSec></langSec></conceptEntry>
	</body></text></tbx>`
	concepts, _, err := ParseConcepts(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("DCA must be accepted, got %v", err)
	}
	if len(concepts) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(concepts))
	}
}

// findEntry returns the first entry matching source+target, or nil.
func findEntry(entries []Entry, source, target string) *Entry {
	for i := range entries {
		if entries[i].SourceTerm == source && entries[i].TargetTerm == target {
			return &entries[i]
		}
	}
	return nil
}

func TestParseEntries_BasicV3(t *testing.T) {
	f, err := os.Open("testdata/tbx_basic_v3.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, _, err := ParseEntries(f, "en", "fr")
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}

	// c1 → 2 target terms (courriel, mail); c2 → 1; c3 has no fr → skipped.
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}

	courriel := findEntry(entries, "e-mail", "courriel")
	if courriel == nil {
		t.Fatal("missing e-mail→courriel")
	}
	if courriel.Status != statusApproved {
		t.Errorf("courriel status = %q, want approved", courriel.Status)
	}
	if courriel.Domain != "software" { // concept-level subjectField
		t.Errorf("courriel domain = %q, want software", courriel.Domain)
	}
	if courriel.Notes != "POS: noun" {
		t.Errorf("courriel notes = %q, want %q", courriel.Notes, "POS: noun")
	}

	mail := findEntry(entries, "e-mail", "mail")
	if mail == nil {
		t.Fatal("missing e-mail→mail")
	}
	if mail.Status != statusForbidden { // deprecatedTerm-admn-sts
		t.Errorf("mail status = %q, want forbidden", mail.Status)
	}

	fed := findEntry(entries, "federated database", "base de données fédérée")
	if fed == nil {
		t.Fatal("missing federated database entry")
	}
	if fed.Domain != "database" { // term-level subjectField on fr
		t.Errorf("fed domain = %q, want database", fed.Domain)
	}

	if findEntry(entries, "orphan term", "Waisenbegriff") != nil {
		t.Error("de term should be skipped when target is fr")
	}
}

func TestParseEntries_LegacyV2Martif(t *testing.T) {
	f, err := os.Open("testdata/tbx_v2_martif.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, _, err := ParseEntries(f, "en", "fr")
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	inv := findEntry(entries, "invoice", "facture")
	if inv == nil {
		t.Fatal("missing invoice→facture (tig)")
	}
	if inv.Domain != "finance" {
		t.Errorf("invoice domain = %q, want finance", inv.Domain)
	}
	if inv.Status != statusApproved {
		t.Errorf("invoice status = %q, want approved", inv.Status)
	}

	// spreadsheet→tableur comes from <ntig><termGrp> nesting.
	if findEntry(entries, "spreadsheet", "tableur") == nil {
		t.Error("missing spreadsheet→tableur (ntig/termGrp)")
	}
}

func TestParseEntries_LanguageSubtagFallback(t *testing.T) {
	// Base uses "fr-FR"/"en-US"; file uses bare "en"/"fr". Primary-subtag
	// matching should still line them up.
	f, err := os.Open("testdata/tbx_basic_v3.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, _, err := ParseEntries(f, "en-US", "fr-FR")
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 with subtag fallback", len(entries))
	}
}

func TestParseEntries_ReportLanguageMismatch(t *testing.T) {
	// Base languages (es→it) match nothing in the file (en/fr/de). The parse
	// should succeed with zero entries but report the languages it saw, so the
	// caller can explain the empty import.
	f, err := os.Open("testdata/tbx_basic_v3.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, report, err := ParseEntries(f, "es", "it")
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for es→it, got %d", len(entries))
	}
	if report.Concepts != 3 {
		t.Errorf("report.Concepts = %d, want 3", report.Concepts)
	}
	if report.MatchedConcepts != 0 {
		t.Errorf("report.MatchedConcepts = %d, want 0", report.MatchedConcepts)
	}
	want := []string{"de", "en", "fr"}
	if len(report.LanguagesSeen) != len(want) {
		t.Fatalf("LanguagesSeen = %v, want %v", report.LanguagesSeen, want)
	}
	for i, l := range want {
		if report.LanguagesSeen[i] != l {
			t.Errorf("LanguagesSeen[%d] = %q, want %q", i, report.LanguagesSeen[i], l)
		}
	}
}

// XTM (and other tools) label data categories differently than the ISO-canonical
// names. The alias registry must capture status via type="status" (VALID/
// FORBIDDEN), domain via type="domain", and remarks.
func TestParseConcepts_XTMDialect(t *testing.T) {
	f, err := os.Open("testdata/tbx_xtm_martif.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	concepts, _, err := ParseConcepts(f)
	if err != nil {
		t.Fatalf("ParseConcepts: %v", err)
	}
	if len(concepts) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(concepts))
	}
	c := concepts[0]
	if c.Domain != "Electrical system;Work object" {
		t.Errorf("domain (type=\"domain\") not captured: %q", c.Domain)
	}
	if c.Definition != "A connector pin." {
		t.Errorf("concept definition not captured: %q", c.Definition)
	}
	if len(c.Terms) != 3 {
		t.Fatalf("expected 3 terms (en pin, es clavija, es pin), got %d", len(c.Terms))
	}
	// Find the forbidden Spanish "clavija".
	var clavija, esPin, enPin *ConceptTerm
	for i := range c.Terms {
		switch {
		case c.Terms[i].Text == "clavija":
			clavija = &c.Terms[i]
		case c.Terms[i].Text == "pin" && c.Terms[i].Language == "es-es":
			esPin = &c.Terms[i]
		case c.Terms[i].Text == "pin" && c.Terms[i].Language == "en-gb":
			enPin = &c.Terms[i]
		}
	}
	if clavija == nil || clavija.Status != statusForbidden {
		t.Errorf("es 'clavija' (type=\"status\">FORBIDDEN) must map to forbidden; got %+v", clavija)
	}
	if enPin == nil || enPin.PartOfSpeech != "noun" {
		t.Errorf("en 'pin' partOfSpeech not captured; got %+v", enPin)
	}
	if esPin == nil || esPin.Status != statusApproved || esPin.Notes == "" {
		t.Errorf("es 'pin' should be approved with remarks note; got %+v", esPin)
	}
}

// The bilingual ParseEntries path shares the alias registry, so the same XTM file
// imports forbidden status + domain correctly into flat entries.
func TestParseEntries_XTMDialectAliases(t *testing.T) {
	f, err := os.Open("testdata/tbx_xtm_martif.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, _, err := ParseEntries(f, "en-GB", "es-ES")
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	// pin → clavija (forbidden) and pin → pin (approved).
	clavija := findEntry(entries, "pin", "clavija")
	if clavija == nil || clavija.Status != statusForbidden {
		t.Errorf("expected pin→clavija forbidden; got %+v", clavija)
	}
	if clavija != nil && clavija.Domain != "Electrical system;Work object" {
		t.Errorf("domain not captured on entry; got %q", clavija.Domain)
	}
	if p := findEntry(entries, "pin", "pin"); p == nil || p.Status != statusApproved {
		t.Errorf("expected pin→pin approved; got %+v", p)
	}
}

// Kaleidoscope/quickTerm (XTM export) dialect: capitalized <descrip type="Domain">,
// term-level status via <termNote type="Usage"> (Preferred/Admitted/Do not use),
// langSet-level <descrip type="Validated"> as the status fallback, content in
// <termNote type="Definition"> etc. Part of speech ("Grammar") is intentionally
// NOT mapped.
func TestParseConcepts_KaleidoscopeDialect(t *testing.T) {
	f, err := os.Open("testdata/tbx_kaleidoscope_martif.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	concepts, _, err := ParseConcepts(f)
	if err != nil {
		t.Fatalf("ParseConcepts: %v", err)
	}
	if len(concepts) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(concepts))
	}
	c := concepts[0]
	if c.Domain != "Regulatory" {
		t.Errorf("domain from capitalized <descrip type=\"Domain\"> not captured: %q", c.Domain)
	}

	byText := map[string]ConceptTerm{}
	for _, tm := range c.Terms {
		byText[tm.Text] = tm
	}

	// Usage=Preferred → approved; Definition → notes; Grammar (POS) dropped.
	if w := byText["warning"]; w.Status != statusApproved {
		t.Errorf("warning: Usage=Preferred should be approved; got %q", w.Status)
	} else {
		if w.PartOfSpeech != "" {
			t.Errorf("warning: part of speech must be dropped (Grammar not mapped); got %q", w.PartOfSpeech)
		}
		if !strings.Contains(w.Notes, "Definition: a statement of danger") {
			t.Errorf("warning: Definition termNote should land in notes; got %q", w.Notes)
		}
		if strings.Contains(strings.ToLower(w.Notes), "origin") {
			t.Errorf("warning: provenance (Origin) must be dropped from notes; got %q", w.Notes)
		}
	}
	// Usage=Do not use → forbidden.
	if byText["caution"].Status != statusForbidden {
		t.Errorf("caution: Usage=Do not use should be forbidden; got %q", byText["caution"].Status)
	}
	// No Usage note → fall back to langSet Validated: No → pending, Yes → approved.
	if byText["Warnung"].Status != statusPending {
		t.Errorf("Warnung: Validated=No (no Usage) should be pending; got %q", byText["Warnung"].Status)
	}
	if byText["avertissement"].Status != statusApproved {
		t.Errorf("avertissement: Validated=Yes (no Usage) should be approved; got %q", byText["avertissement"].Status)
	}
}

// Full multilingual concept export → re-parse must preserve languages, synonyms,
// per-term status (incl. forbidden), POS and notes.
// An unrecognized administrative status must import as pending (don't guess
// "approved"); an absent status is approved (unmarked = usable glossary term).
func TestParseConcepts_StatusDefaults(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<tbx style="dca" type="TBX-Basic" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
  <text><body>
    <conceptEntry id="c1">
      <langSec xml:lang="en"><termSec>
        <term>widget</term>
        <termNote type="administrativeStatus">someCustomStatus</termNote>
      </termSec></langSec>
      <langSec xml:lang="fr"><termSec><term>machin</term></termSec></langSec>
    </conceptEntry>
  </body></text>
</tbx>`
	concepts, _, err := ParseConcepts(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(concepts))
	}
	var widget, machin *ConceptTerm
	for i := range concepts[0].Terms {
		switch concepts[0].Terms[i].Text {
		case "widget":
			widget = &concepts[0].Terms[i]
		case "machin":
			machin = &concepts[0].Terms[i]
		}
	}
	if widget == nil || widget.Status != statusPending {
		t.Errorf("unrecognized status must map to pending; got %+v", widget)
	}
	if machin == nil || machin.Status != statusApproved {
		t.Errorf("absent status must map to approved; got %+v", machin)
	}
}

// Microsoft's export wraps the definition in <descripGrp> on the source-language
// section. The parser must descend into descripGrp and surface it as the concept
// definition.
func TestParseConcepts_MicrosoftDescripGrp(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<martif type="TBX" xml:lang="en-US"><text><body>
  <termEntry id="1000_1_2">
    <langSet xml:lang="en-US">
      <descripGrp><descrip type="definition">A pin connector.</descrip></descripGrp>
      <ntig><termGrp><term id="1">Point</term><termNote type="partOfSpeech">Noun</termNote></termGrp></ntig>
    </langSet>
    <langSet xml:lang="af">
      <ntig><termGrp><term id="2">Punt</term></termGrp></ntig>
    </langSet>
  </termEntry>
</body></text></martif>`
	concepts, _, err := ParseConcepts(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(concepts))
	}
	if concepts[0].Definition != "A pin connector." {
		t.Errorf("descripGrp-wrapped definition not captured: %q", concepts[0].Definition)
	}
	// No status markers → all approved (Microsoft's collection is all approved).
	for _, tm := range concepts[0].Terms {
		if tm.Status != statusApproved {
			t.Errorf("unmarked term should be approved; got %q for %q", tm.Status, tm.Text)
		}
	}
}

// Termcat-style export: genuinely multilingual, and the definition lives in a
// <descripGrp> on a *non-first* langSet (Catalan), not the source-language one.
// The parser must find the descripGrp definition wherever it sits and promote it
// to the concept definition. Complements TestParseConcepts_MicrosoftDescripGrp,
// which covers descripGrp on the first/source langSet.
func TestParseConcepts_TermcatDescripGrpFile(t *testing.T) {
	f, err := os.Open("testdata/tbx_termcat_descripgrp.tbx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	concepts, _, err := ParseConcepts(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts) != 2 {
		t.Fatalf("expected 2 concepts, got %d", len(concepts))
	}

	c := concepts[0]
	if c.Domain != "Esports > Esports d'aventura" {
		t.Errorf("hierarchical subjectField not captured/unescaped: %q", c.Domain)
	}
	// Definition is on the Catalan langSet, wrapped in descripGrp — not the first
	// language in the entry (French is). It must still surface as the concept def.
	if c.Definition == "" {
		t.Errorf("descripGrp definition on a non-first langSet was dropped")
	}
	if len(c.Terms) != 4 {
		t.Fatalf("expected 4 languages in concept 1, got %d", len(c.Terms))
	}
	langs := map[string]bool{}
	for _, tm := range c.Terms {
		langs[tm.Language] = true
		// No status markers anywhere → everything approved.
		if tm.Status != statusApproved {
			t.Errorf("unmarked term %q should be approved, got %q", tm.Text, tm.Status)
		}
	}
	for _, want := range []string{"fr", "ca", "en", "es"} {
		if !langs[want] {
			t.Errorf("missing language %q in concept 1", want)
		}
	}

	// Second concept: definition also in a descripGrp, only the Catalan section
	// carries it, and there's no French — proves promotion isn't tied to ordering.
	if concepts[1].Definition == "" {
		t.Errorf("concept 2 descripGrp definition was dropped")
	}
}

func TestExportConceptsRoundTrip(t *testing.T) {
	in := []Concept{{
		Domain:     "software",
		Definition: "A secret credential.",
		Terms: []ConceptTerm{
			{Language: "en", Text: "password", Status: statusApproved, PartOfSpeech: "noun"},
			{Language: "es", Text: "contraseña", Status: statusApproved},
			{Language: "es", Text: "clave", Status: statusForbidden},
			{Language: "fr", Text: "mot de passe", Status: statusApproved, Notes: "common register"},
		},
	}}

	var buf bytes.Buffer
	if err := WriteHeader(&buf, "en"); err != nil {
		t.Fatal(err)
	}
	if err := WriteConceptEntries(&buf, in, 0); err != nil {
		t.Fatal(err)
	}
	if err := WriteFooter(&buf); err != nil {
		t.Fatal(err)
	}

	out, _, err := ParseConcepts(&buf)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 concept, got %d", len(out))
	}
	c := out[0]
	if c.Domain != "software" {
		t.Errorf("domain lost: %q", c.Domain)
	}
	if c.Definition != "A secret credential." {
		t.Errorf("definition lost: %q", c.Definition)
	}
	if len(c.Terms) != 4 {
		t.Fatalf("expected 4 terms round-tripped, got %d", len(c.Terms))
	}
	var forbidden, pos, note bool
	for _, tm := range c.Terms {
		if tm.Text == "clave" && tm.Language == "es" && tm.Status == statusForbidden {
			forbidden = true
		}
		if tm.Text == "password" && tm.PartOfSpeech == "noun" {
			pos = true
		}
		if tm.Text == "mot de passe" && tm.Notes == "common register" {
			note = true
		}
	}
	if !forbidden {
		t.Error("forbidden 'clave' didn't round-trip")
	}
	if !pos {
		t.Error("partOfSpeech didn't round-trip")
	}
	if !note {
		t.Error("note didn't round-trip")
	}
}

func TestExportRoundTrip(t *testing.T) {
	in := []Entry{
		{SourceTerm: "e-mail", TargetTerm: "courriel", Status: statusApproved, Notes: "POS: noun", Domain: "software"},
		{SourceTerm: "e-mail", TargetTerm: "mail", Status: statusForbidden},
		{SourceTerm: "a & b", TargetTerm: "a <b>", Status: statusApproved}, // XML escaping
	}

	var buf bytes.Buffer
	if err := Export(&buf, "en", "fr", in); err != nil {
		t.Fatalf("Export: %v", err)
	}

	out, _, err := ParseEntries(&buf, "en", "fr")
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip: got %d entries, want %d", len(out), len(in))
	}

	for _, want := range in {
		got := findEntry(out, want.SourceTerm, want.TargetTerm)
		if got == nil {
			t.Errorf("round-trip lost %q→%q", want.SourceTerm, want.TargetTerm)
			continue
		}
		if got.Status != want.Status {
			t.Errorf("%q→%q status = %q, want %q", want.SourceTerm, want.TargetTerm, got.Status, want.Status)
		}
		if got.Domain != want.Domain {
			t.Errorf("%q→%q domain = %q, want %q", want.SourceTerm, want.TargetTerm, got.Domain, want.Domain)
		}
		if got.Notes != want.Notes {
			t.Errorf("%q→%q notes = %q, want %q", want.SourceTerm, want.TargetTerm, got.Notes, want.Notes)
		}
	}
}

func TestParseEntries_UTF16BOM(t *testing.T) {
	raw, err := os.ReadFile("testdata/tbx_basic_v3.tbx")
	if err != nil {
		t.Fatal(err)
	}
	// Encode to UTF-16 LE with BOM.
	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
	u16, _, err := transform.Bytes(enc, raw)
	if err != nil {
		t.Fatalf("encode utf-16: %v", err)
	}

	entries, _, err := ParseEntries(bytes.NewReader(u16), "en", "fr")
	if err != nil {
		t.Fatalf("ParseEntries (utf-16): %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("utf-16: got %d entries, want 3", len(entries))
	}
}
