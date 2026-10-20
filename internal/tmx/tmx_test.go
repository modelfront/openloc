package tmx

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestParseSampleTMX(t *testing.T) {
	f, err := os.Open("testdata/sample.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	if tmxFile.Header.SrcLang != "en" {
		t.Errorf("expected srclang 'en', got %q", tmxFile.Header.SrcLang)
	}
	if len(tmxFile.Units) != 5 {
		t.Fatalf("expected 5 TUs, got %d", len(tmxFile.Units))
	}

	// TU 1: en, es, fr
	tu1 := tmxFile.Units[0]
	if len(tu1.Variants) != 3 {
		t.Errorf("TU1: expected 3 variants, got %d", len(tu1.Variants))
	}
	if tu1.Variants[0].Lang != "en" || tu1.Variants[0].Text != "Hello world" {
		t.Errorf("TU1 source: got lang=%q text=%q", tu1.Variants[0].Lang, tu1.Variants[0].Text)
	}

	// TU 3: en + es only (no fr)
	tu3 := tmxFile.Units[2]
	if len(tu3.Variants) != 2 {
		t.Errorf("TU3: expected 2 variants, got %d", len(tu3.Variants))
	}
}

func TestParseInlineMarkup(t *testing.T) {
	f, err := os.Open("testdata/sample.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	// TU 4 has <bpt> and <ept> inline tags. Per the post-migration contract:
	//   Variant.Text     = plain text with tags STRIPPED (used for matching)
	//   Variant.RichText = raw inner XML with tags PRESERVED (used for display)
	tu4 := tmxFile.Units[3]
	enVariant := tu4.Variants[0]

	// Plain Text: tags + their content removed; surrounding text survives.
	if strings.Contains(enVariant.Text, "<bpt") || strings.Contains(enVariant.Text, "<ept") {
		t.Errorf("expected inline tags STRIPPED from plain Text, got: %s", enVariant.Text)
	}
	if !strings.Contains(enVariant.Text, "Important") {
		t.Errorf("expected 'Important' text in plain form, got: %s", enVariant.Text)
	}

	// RichText: tags preserved verbatim for the display layer.
	if !strings.Contains(enVariant.RichText, "<bpt") {
		t.Errorf("expected inline <bpt> tag preserved in RichText, got: %s", enVariant.RichText)
	}
	if !strings.Contains(enVariant.RichText, "<ept") {
		t.Errorf("expected inline <ept> tag preserved in RichText, got: %s", enVariant.RichText)
	}
}

func TestLanguageCodeLowercasing(t *testing.T) {
	f, err := os.Open("testdata/sample.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	// TU 5 has EN-US, es-MX, fr-CA
	tu5 := tmxFile.Units[4]
	langs := make(map[string]bool)
	for _, v := range tu5.Variants {
		langs[v.Lang] = true
	}
	if !langs["en-us"] {
		t.Error("expected 'en-us' (lowercased from EN-US)")
	}
	if !langs["es-mx"] {
		t.Error("expected 'es-mx' (lowercased from es-MX)")
	}
	if !langs["fr-ca"] {
		t.Error("expected 'fr-ca' (lowercased from fr-CA)")
	}
}

func TestFlattenEntries(t *testing.T) {
	f, err := os.Open("testdata/sample.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	entries := FlattenEntries(tmxFile)

	// TU1: 2 targets (es, fr), TU2: 2 targets, TU3: 1 target (es), TU4: 1 target (es)
	// TU5: srclang is "en" and TU5's source tuv is "en-us" — a base-code match,
	// so the unit contributes its 2 targets (es-mx, fr-ca) instead of being dropped.
	// Total: 2+2+1+1+2 = 8
	if len(entries) != 8 {
		t.Fatalf("expected 8 entries, got %d", len(entries))
	}

	if entries[0].SourceLang != "en" || entries[0].TargetLang != "es" {
		t.Errorf("entry[0]: expected en→es, got %s→%s", entries[0].SourceLang, entries[0].TargetLang)
	}
	if entries[0].SourceText != "Hello world" || entries[0].TargetText != "Hola mundo" {
		t.Errorf("entry[0]: unexpected texts: %q / %q", entries[0].SourceText, entries[0].TargetText)
	}

	// Inline markup lives on RichText; SourceText/TargetText are plain.
	foundRich := false
	for _, e := range entries {
		if strings.Contains(e.SourceRichText, "<bpt") {
			foundRich = true
		}
		if strings.Contains(e.SourceText, "<bpt") {
			t.Errorf("entry's plain SourceText must NOT contain inline tags, got: %s", e.SourceText)
		}
	}
	if !foundRich {
		t.Error("expected inline markup preserved in SourceRichText of flattened entries")
	}
}

func TestFlattenEntriesCustomSrcLang(t *testing.T) {
	// TMX where srclang uses regional code "en-us"
	input := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en-US" datatype="plaintext" segtype="sentence" adminlang="en"/>
  <body>
    <tu>
      <tuv xml:lang="en-US"><seg>Hello</seg></tuv>
      <tuv xml:lang="es"><seg>Hola</seg></tuv>
    </tu>
  </body>
</tmx>`

	tmxFile, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	entries := FlattenEntries(tmxFile)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SourceLang != "en-us" || entries[0].TargetLang != "es" {
		t.Errorf("expected en-us→es, got %s→%s", entries[0].SourceLang, entries[0].TargetLang)
	}
}

func TestExportRoundTrip(t *testing.T) {
	entries := []Entry{
		{SourceLang: "en", TargetLang: "es", SourceText: "Hello", TargetText: "Hola"},
		{SourceLang: "en", TargetLang: "fr", SourceText: "Hello", TargetText: "Bonjour"},
		{SourceLang: "en", TargetLang: "es", SourceText: "Goodbye", TargetText: "Adiós"},
	}

	var buf bytes.Buffer
	if err := Export(&buf, "en", entries); err != nil {
		t.Fatal(err)
	}

	tmxFile, err := Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if tmxFile.Header.SrcLang != "en" {
		t.Errorf("round-trip srclang: expected 'en', got %q", tmxFile.Header.SrcLang)
	}

	// Each entry becomes its own TU (streaming-friendly, no grouping)
	if len(tmxFile.Units) != 3 {
		t.Fatalf("round-trip: expected 3 TUs, got %d", len(tmxFile.Units))
	}

	reEntries := FlattenEntries(tmxFile)
	if len(reEntries) != 3 {
		t.Fatalf("round-trip: expected 3 entries, got %d", len(reEntries))
	}
	for i, want := range entries {
		got := reEntries[i]
		if got.SourceLang != want.SourceLang || got.TargetLang != want.TargetLang ||
			got.SourceText != want.SourceText || got.TargetText != want.TargetText {
			t.Errorf("round-trip entry %d = %s→%s %q→%q, want %s→%s %q→%q", i,
				got.SourceLang, got.TargetLang, got.SourceText, got.TargetText,
				want.SourceLang, want.TargetLang, want.SourceText, want.TargetText)
		}
	}
}

func TestExportInlineMarkup(t *testing.T) {
	entries := []Entry{
		{
			SourceLang: "en", TargetLang: "es",
			SourceText: `<bpt i="1" type="bold">&lt;b&gt;</bpt>Bold<ept i="1">&lt;/b&gt;</ept>`,
			TargetText: `<bpt i="1" type="bold">&lt;b&gt;</bpt>Negrita<ept i="1">&lt;/b&gt;</ept>`,
		},
	}

	var buf bytes.Buffer
	if err := Export(&buf, "en", entries); err != nil {
		t.Fatal(err)
	}

	tmxFile, err := Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}

	reEntries := FlattenEntries(tmxFile)
	if len(reEntries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(reEntries))
	}
	// Inline markup survives the round-trip via SourceRichText (post-migration
	// shape). The plain SourceText is the stripped form.
	if !strings.Contains(reEntries[0].SourceRichText, "<bpt") {
		t.Errorf("inline markup lost in round-trip — SourceRichText: %s", reEntries[0].SourceRichText)
	}
	if strings.Contains(reEntries[0].SourceText, "<bpt") {
		t.Errorf("plain SourceText must NOT contain inline tags after round-trip, got: %s", reEntries[0].SourceText)
	}
}

func TestParseEmptyBody(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"/>
  <body/>
</tmx>`

	tmxFile, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(tmxFile.Units) != 0 {
		t.Errorf("expected 0 TUs, got %d", len(tmxFile.Units))
	}
}

func TestParseEmptySegSkipped(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"/>
  <body>
    <tu>
      <tuv xml:lang="en"><seg>Hello</seg></tuv>
      <tuv xml:lang="es"><seg></seg></tuv>
    </tu>
  </body>
</tmx>`

	tmxFile, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	// TU should have only 1 variant (empty seg skipped)
	if len(tmxFile.Units) != 1 {
		t.Fatalf("expected 1 TU, got %d", len(tmxFile.Units))
	}
	if len(tmxFile.Units[0].Variants) != 1 {
		t.Errorf("expected 1 variant (empty skipped), got %d", len(tmxFile.Units[0].Variants))
	}
}

func TestParseUTF16LE(t *testing.T) {
	f, err := os.Open("testdata/utf16le.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatalf("failed to parse UTF-16 LE TMX: %v", err)
	}

	if tmxFile.Header.SrcLang != "en" {
		t.Errorf("expected srclang 'en', got %q", tmxFile.Header.SrcLang)
	}
	if len(tmxFile.Units) != 2 {
		t.Fatalf("expected 2 TUs, got %d", len(tmxFile.Units))
	}

	entries := FlattenEntries(tmxFile)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].SourceText != "Good morning" || entries[0].TargetText != "Buenos días" {
		t.Errorf("entry 0: got src=%q tgt=%q", entries[0].SourceText, entries[0].TargetText)
	}
	if entries[1].SourceText != "Thank you" || entries[1].TargetText != "Gracias" {
		t.Errorf("entry 1: got src=%q tgt=%q", entries[1].SourceText, entries[1].TargetText)
	}
}

func TestParseUTF16BE(t *testing.T) {
	f, err := os.Open("testdata/utf16be.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tmxFile, err := Parse(f)
	if err != nil {
		t.Fatalf("failed to parse UTF-16 BE TMX: %v", err)
	}

	if tmxFile.Header.SrcLang != "en" {
		t.Errorf("expected srclang 'en', got %q", tmxFile.Header.SrcLang)
	}
	if len(tmxFile.Units) != 2 {
		t.Fatalf("expected 2 TUs, got %d", len(tmxFile.Units))
	}

	entries := FlattenEntries(tmxFile)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].SourceText != "Good morning" || entries[0].TargetText != "Buenos días" {
		t.Errorf("entry 0: got src=%q tgt=%q", entries[0].SourceText, entries[0].TargetText)
	}
}

func TestStreamingExport(t *testing.T) {
	// WriteHeader + WriteEntries (called twice) + WriteFooter should produce valid TMX
	entries1 := []Entry{
		{SourceLang: "en", TargetLang: "es", SourceText: "Hello", TargetText: "Hola"},
	}
	entries2 := []Entry{
		{SourceLang: "en", TargetLang: "fr", SourceText: "Goodbye", TargetText: "Au revoir"},
	}

	var buf bytes.Buffer
	if err := WriteHeader(&buf, "en"); err != nil {
		t.Fatal(err)
	}
	if err := WriteEntries(&buf, entries1); err != nil {
		t.Fatal(err)
	}
	if err := WriteEntries(&buf, entries2); err != nil {
		t.Fatal(err)
	}
	if err := WriteFooter(&buf); err != nil {
		t.Fatal(err)
	}

	// Re-parse to verify valid TMX
	tmxFile, err := Parse(&buf)
	if err != nil {
		t.Fatalf("streaming export produced invalid TMX: %v", err)
	}
	if len(tmxFile.Units) != 2 {
		t.Errorf("expected 2 TUs, got %d", len(tmxFile.Units))
	}
	reEntries := FlattenEntries(tmxFile)
	if len(reEntries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(reEntries))
	}
}

func TestParseMalformedXML(t *testing.T) {
	input := `not valid xml at all`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Error("expected error for malformed XML")
	}
}

func TestParseEntriesStreaming(t *testing.T) {
	f, err := os.Open("testdata/sample.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, err := ParseEntries(f)
	if err != nil {
		t.Fatal(err)
	}

	// sample.tmx has 5 TUs with source "en" and targets "es", "fr"
	// TU 1: en→es, en→fr (2 entries)
	// TU 2: en→es, en→fr (2 entries)
	// TU 3: en→es, en→fr (2 entries)
	// TU 4: en→es (1 entry, no fr variant)
	// TU 5: en→es, en→fr (2 entries)
	// Total: 9 entries

	// Compare with Parse+FlattenEntries to ensure parity
	f2, _ := os.Open("testdata/sample.tmx")
	defer f2.Close()
	tmxFile, _ := Parse(f2)
	expected := FlattenEntries(tmxFile)

	if len(entries) != len(expected) {
		t.Fatalf("ParseEntries returned %d entries, Parse+Flatten returned %d", len(entries), len(expected))
	}

	for i := range entries {
		if entries[i].SourceLang != expected[i].SourceLang {
			t.Errorf("[%d] SourceLang: got %q, want %q", i, entries[i].SourceLang, expected[i].SourceLang)
		}
		if entries[i].TargetLang != expected[i].TargetLang {
			t.Errorf("[%d] TargetLang: got %q, want %q", i, entries[i].TargetLang, expected[i].TargetLang)
		}
		if entries[i].SourceText != expected[i].SourceText {
			t.Errorf("[%d] SourceText: got %q, want %q", i, entries[i].SourceText, expected[i].SourceText)
		}
		if entries[i].TargetText != expected[i].TargetText {
			t.Errorf("[%d] TargetText: got %q, want %q", i, entries[i].TargetText, expected[i].TargetText)
		}
	}
}

func TestParseEntriesUTF16(t *testing.T) {
	f, err := os.Open("testdata/utf16le.tmx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entries, err := ParseEntries(f)
	if err != nil {
		t.Fatalf("ParseEntries UTF-16 LE: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries from UTF-16 LE file")
	}
	if entries[0].SourceLang != "en" {
		t.Errorf("SourceLang = %q, want en", entries[0].SourceLang)
	}
}

func TestParseEntriesMetadata(t *testing.T) {
	tmxData := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu tuid="tu-001" creationdate="20260115T120000Z" creationid="translator1" changedate="20260320T140000Z" changeid="reviewer1">
      <tuv xml:lang="en"><seg>Hello world</seg></tuv>
      <tuv xml:lang="fr"><seg>Bonjour le monde</seg></tuv>
    </tu>
    <tu tuid="tu-002" creationid="translator2">
      <tuv xml:lang="en"><seg>Goodbye</seg></tuv>
      <tuv xml:lang="fr"><seg>Au revoir</seg></tuv>
    </tu>
  </body>
</tmx>`

	entries, err := ParseEntries(strings.NewReader(tmxData))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// First entry should have full metadata
	e1 := entries[0]
	if e1.TUId != "tu-001" {
		t.Errorf("TUId = %q, want tu-001", e1.TUId)
	}
	if e1.CreationDate != "20260115T120000Z" {
		t.Errorf("CreationDate = %q, want 20260115T120000Z", e1.CreationDate)
	}
	if e1.Creator != "translator1" {
		t.Errorf("Creator = %q, want translator1", e1.Creator)
	}
	if e1.ChangeDate != "20260320T140000Z" {
		t.Errorf("ChangeDate = %q, want 20260320T140000Z", e1.ChangeDate)
	}
	if e1.ChangeUser != "reviewer1" {
		t.Errorf("ChangeUser = %q, want reviewer1", e1.ChangeUser)
	}

	// Second entry: partial metadata
	e2 := entries[1]
	if e2.TUId != "tu-002" {
		t.Errorf("TUId = %q, want tu-002", e2.TUId)
	}
	if e2.Creator != "translator2" {
		t.Errorf("Creator = %q, want translator2", e2.Creator)
	}
	if e2.ChangeDate != "" {
		t.Errorf("ChangeDate should be empty, got %q", e2.ChangeDate)
	}
}

func TestTMXMetadataRoundTrip(t *testing.T) {
	entries := []Entry{
		{
			SourceLang:   "en",
			TargetLang:   "fr",
			SourceText:   "Hello",
			TargetText:   "Bonjour",
			TUId:         "tu-100",
			CreationDate: "20260115T120000Z",
			Creator:      "translator1",
			ChangeDate:   "20260320T140000Z",
			ChangeUser:   "reviewer1",
		},
		{
			SourceLang: "en",
			TargetLang: "fr",
			SourceText: "Goodbye",
			TargetText: "Au revoir",
			// No metadata — should export without attributes
		},
	}

	var buf bytes.Buffer
	if err := Export(&buf, "en", entries); err != nil {
		t.Fatal(err)
	}
	tmxOutput := buf.String()

	if !strings.Contains(tmxOutput, `tuid="tu-100"`) {
		t.Error("export missing tuid attribute")
	}
	if !strings.Contains(tmxOutput, `creationdate="20260115T120000Z"`) {
		t.Error("export missing creationdate attribute")
	}
	if !strings.Contains(tmxOutput, `creationid="translator1"`) {
		t.Error("export missing creationid attribute")
	}
	if !strings.Contains(tmxOutput, `changedate="20260320T140000Z"`) {
		t.Error("export missing changedate attribute")
	}
	if !strings.Contains(tmxOutput, `changeid="reviewer1"`) {
		t.Error("export missing changeid attribute")
	}

	// Second entry should NOT have tuid (empty)
	parsedEntries, err := ParseEntries(strings.NewReader(tmxOutput))
	if err != nil {
		t.Fatalf("re-parse exported TMX: %v", err)
	}
	if len(parsedEntries) != 2 {
		t.Fatalf("expected 2 entries after round-trip, got %d", len(parsedEntries))
	}

	// First entry metadata should survive round-trip
	rt := parsedEntries[0]
	if rt.TUId != "tu-100" {
		t.Errorf("round-trip TUId = %q, want tu-100", rt.TUId)
	}
	if rt.Creator != "translator1" {
		t.Errorf("round-trip Creator = %q, want translator1", rt.Creator)
	}
	if rt.ChangeDate != "20260320T140000Z" {
		t.Errorf("round-trip ChangeDate = %q, want 20260320T140000Z", rt.ChangeDate)
	}
}

func TestParseEntriesMultipleTranslations(t *testing.T) {
	// Same source text with different targets — both should be preserved
	tmxData := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu tuid="tu-1">
      <tuv xml:lang="en"><seg>Hello</seg></tuv>
      <tuv xml:lang="fr"><seg>Bonjour</seg></tuv>
    </tu>
    <tu tuid="tu-2">
      <tuv xml:lang="en"><seg>Hello</seg></tuv>
      <tuv xml:lang="fr"><seg>Salut</seg></tuv>
    </tu>
    <tu tuid="tu-3">
      <tuv xml:lang="en"><seg>Hello</seg></tuv>
      <tuv xml:lang="fr"><seg>Bonjour</seg></tuv>
    </tu>
  </body>
</tmx>`

	entries, err := ParseEntries(strings.NewReader(tmxData))
	if err != nil {
		t.Fatal(err)
	}

	// All 3 TUs should produce entries (even though tu-1 and tu-3 have same source+target)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (parser preserves all), got %d", len(entries))
	}

	// Verify both translations present
	targets := map[string]bool{}
	for _, e := range entries {
		targets[e.TargetText] = true
	}
	if !targets["Bonjour"] {
		t.Error("missing target 'Bonjour'")
	}
	if !targets["Salut"] {
		t.Error("missing target 'Salut'")
	}
}

// Language codes must land in the canonical langcode form, not merely
// lowercased: `en_US` keeps its underscore under strings.ToLower, and
// split_part(source_language, '-', 1) in the TM lookups then never yields a
// base code, so those rows are invisible to every lookup.
func TestParseEntriesNormalizesLanguageCodes(t *testing.T) {
	tmxData := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="EN_US" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu tuid="1">
      <tuv xml:lang="en_US"><seg>File not found</seg></tuv>
      <tuv xml:lang="fr_CA"><seg>Fichier non trouvé</seg></tuv>
      <tuv xml:lang="IW"><seg>הקובץ לא נמצא</seg></tuv>
    </tu>
  </body>
</tmx>`

	entries, err := ParseEntries(strings.NewReader(tmxData))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (fr-ca, he), got %d", len(entries))
	}

	targets := map[string]string{}
	for _, e := range entries {
		if e.SourceLang != "en-us" {
			t.Errorf("SourceLang = %q, want %q (normalized from EN_US)", e.SourceLang, "en-us")
		}
		targets[e.TargetLang] = e.TargetText
	}
	if _, ok := targets["fr-ca"]; !ok {
		t.Errorf("missing target %q (normalized from fr_CA), got %v", "fr-ca", targets)
	}
	// Legacy ISO code: `iw` is aliased to `he` by langcode.Normalize.
	if _, ok := targets["he"]; !ok {
		t.Errorf("missing target %q (aliased from IW), got %v", "he", targets)
	}
}

// Pins that a TU whose source tuv is a locale variant of the header srclang
// still pairs instead of being dropped from the import.
func TestParseEntriesSourceVariantPairing(t *testing.T) {
	tmxData := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu tuid="1">
      <tuv xml:lang="en-US"><seg>Trash</seg></tuv>
      <tuv xml:lang="fr"><seg>Corbeille</seg></tuv>
    </tu>
    <tu tuid="2">
      <tuv xml:lang="en-US"><seg>Color</seg></tuv>
      <tuv xml:lang="en-GB"><seg>Colour</seg></tuv>
      <tuv xml:lang="fr"><seg>Couleur</seg></tuv>
    </tu>
    <tu tuid="3">
      <tuv xml:lang="en"><seg>Save</seg></tuv>
      <tuv xml:lang="en-US"><seg>Save</seg></tuv>
      <tuv xml:lang="fr"><seg>Enregistrer</seg></tuv>
    </tu>
  </body>
</tmx>`

	entries, err := ParseEntries(strings.NewReader(tmxData))
	if err != nil {
		t.Fatal(err)
	}

	// One entry per TU: the source is the (only) same-base tuv, and every other
	// same-base tuv is skipped rather than paired as a target — otherwise TU 2
	// would emit a junk en-us→en-gb "translation".
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (one per TU), got %d: %+v", len(entries), entries)
	}

	bySource := map[string]Entry{}
	for _, e := range entries {
		if e.TargetLang != "fr" {
			t.Errorf("unexpected target %q for source %q — same-base tuvs must not become targets",
				e.TargetLang, e.SourceText)
		}
		bySource[e.SourceText] = e
	}

	// The entry stores the tuv's own locale, not the header's srclang.
	if got := bySource["Trash"].SourceLang; got != "en-us" {
		t.Errorf("TU 1 SourceLang = %q, want %q (the tuv's own locale)", got, "en-us")
	}
	if got := bySource["Color"].SourceLang; got != "en-us" {
		t.Errorf("TU 2 SourceLang = %q, want %q", got, "en-us")
	}
	// TU 3 has an exact srclang match alongside a variant — exact wins.
	if got := bySource["Save"].SourceLang; got != "en" {
		t.Errorf("TU 3 SourceLang = %q, want %q (exact srclang match preferred)", got, "en")
	}
}
