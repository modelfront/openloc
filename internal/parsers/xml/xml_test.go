package xml

import (
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"openloc/internal/parsers/common"
	"openloc/internal/wordcount"
)

// The ST4 fixtures: a SCHEMA ST4 source export and the reference XLIFF, which
// is an external CAT tool's segmentation of that source. Our output must match
// it — it is the acceptance spec for this parser.
const (
	st4SourcePath    = "testdata/st4_source.xml"
	st4ReferencePath = "testdata/st4_reference.xlf"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

// referenceSources extracts the <source> text of every trans-unit in the
// reference XLIFF, with inline tags stripped but their content kept, so it can
// be compared against our plain segment text.
//
// Must read innerxml rather than unmarshalling into a string: a plain string
// field silently drops the content of nested elements, turning
// `The F7<g id="1">m</g> Flow Sensor` into `The F7 Flow Sensor`.
func referenceSources(t *testing.T) []string {
	t.Helper()

	var doc struct {
		Units []struct {
			Source struct {
				Inner string `xml:",innerxml"`
			} `xml:"source"`
		} `xml:"file>body>trans-unit"`
	}
	if err := xml.Unmarshal(readFixture(t, st4ReferencePath), &doc); err != nil {
		t.Fatalf("parse reference xliff: %v", err)
	}

	out := make([]string, 0, len(doc.Units))
	for _, u := range doc.Units {
		stripped := xliffTagRE.ReplaceAllString(u.Source.Inner, "")
		decoded, _ := common.XMLUnescape(stripped)
		out = append(out, strings.TrimSpace(decoded))
	}
	return out
}

// xliffTagRE matches the inline tags the reference XLIFF carries inside <source>.
var xliffTagRE = regexp.MustCompile(`</?[gx][^>]*>`)

// TestXMLParseMatchesReference is the test that carries this feature.
//
// It pins segmentation, sentence splitting, whitespace trimming, wrapper-tag
// unwrapping and ordering against the reference XLIFF, the segmentation an
// existing TM would have been built from. A regression anywhere in the extraction rules shows up
// here as a concrete diff rather than a vague behaviour change.
func TestXMLParseMatchesReference(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := referenceSources(t)
	got := make([]string, 0, len(segments))
	for _, s := range segments {
		// The reference strips inline tags from the plain view; our Text field
		// already holds the tag-free form, but <br/> contributes a newline
		// that the reference renders as an empty <x/>.
		got = append(got, strings.TrimSpace(strings.ReplaceAll(s.Text, "\n", "")))
	}

	// Collect EVERY divergence rather than stopping at the first — with an
	// intentional allow-list in play, "the next one after the one I know
	// about" is the interesting signal.
	diverged := 0
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] == want[i] {
			continue
		}
		expected, allowed := intentionalDivergences[want[i]]
		if allowed && got[i] == expected {
			diverged++
			continue
		}
		t.Errorf("segment %d differs:\n got: %q\nwant: %q\ncontext (got):  %q\ncontext (want): %q",
			i, got[i], want[i], sliceAround(got, i), sliceAround(want, i))
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments, want %d\nextra/missing tail (got):  %q\ntail (want): %q",
			len(got), len(want), tail(got, len(want)), tail(want, len(got)))
	}
	// Pin the count so a divergence that stops happening is noticed too — if
	// cross-references go back to being translatable, this drops to 0.
	if diverged != len(intentionalDivergences) {
		t.Errorf("matched %d intentional divergences, want %d — the allow-list is stale",
			diverged, len(intentionalDivergences))
	}
}

// intentionalDivergences maps a reference trans-unit source to what WE produce
// instead, for the three units where we deliberately disagree with it.
//
// Cross-references: the reference exposes each ST4 `<meta-ref>` as a
// translatable paired tag, so its generated text ("Eco"/"Boost") lands in the
// segment. We model it as a non-translatable placeholder, matching how a TMX
// from the same tool carries it (`<ph>`) and the fact that the referenced node
// is separately translated. That alignment is what turns these from fuzzy into
// exact matches against an existing TM — see the ADR for the evidence and the
// open ST4 question.
//
// Everything else in the reference must still match byte for byte.
var intentionalDivergences = map[string]string{
	"For H2O, the pressure correction can be set to Eco or Boost.": "For H2O, the pressure correction can be set to  or .",
	"Eco: q [l/min] = k [%]* (p_in - p_H2O)/100.":                  ": q [l/min] = k [%]* (p_in - p_H2O)/100.",
	"Boost: q [l/min] =k[%]* p_in/100":                             ": q [l/min] =k[%]* p_in/100",
}

func sliceAround(ss []string, i int) []string {
	lo, hi := i-2, i+3
	if lo < 0 {
		lo = 0
	}
	if hi > len(ss) {
		hi = len(ss)
	}
	return ss[lo:hi]
}

func tail(ss []string, from int) []string {
	if from >= len(ss) {
		return nil
	}
	return ss[from:]
}

// TestXMLReconstructWithoutTranslationsIsByteIdentical is the guard that keeps
// the byte-splice honest. The ST4 file carries xml:space="preserve", a
// DOCTYPE, a 25-line settings comment and a file-hash attribute — a parser
// that re-serializes instead of splicing fails this immediately.
func TestXMLReconstructWithoutTranslationsIsByteIdentical(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	out, err := XML{}.Reconstruct(original, map[string]string{})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if string(out) != string(original) {
		t.Errorf("reconstruct with no translations changed the document (%d bytes in, %d out)\n%s",
			len(original), len(out), firstDiff(string(original), string(out)))
	}
}

func firstDiff(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 60
			if lo < 0 {
				lo = 0
			}
			return "first diff at byte " + itoa(i) + ":\n  original: ..." +
				a[lo:min(i+60, len(a))] + "...\n  rebuilt:  ..." + b[lo:min(i+60, len(b))] + "..."
		}
	}
	return "documents share a prefix; lengths differ"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// TestXMLRoundTripWithTranslations covers the real export path: translate a
// representative spread of segments, rebuild, and re-parse the result.
func TestXMLRoundTripWithTranslations(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	byText := map[string]common.Segment{}
	for _, s := range segments {
		if _, seen := byText[s.Text]; !seen {
			byText[s.Text] = s
		}
	}

	// A plain segment, a sentence-split one, and one carrying a <sup> chip.
	// The   are real non-breaking spaces in the source document — the
	// authoring tool uses them to keep standard numbers on one line.
	plain := mustSegment(t, byText, "Addendum to the Instructions for Use")
	split := mustSegment(t, byText, "The indicated accuracies relate to the steady-state flow rate.")
	tagged := mustSegment(t, byText, "The F7m Flow Sensor (100234) complies with ISO 4064-2-55:2018 AMD 1:2023.")

	if tagged.RichText == "" {
		t.Fatalf("expected rich text with a <sup> chip, got none for %q", tagged.Text)
	}

	translations := map[string]string{
		plain.Key:  "Nachtrag zur Gebrauchsanweisung",
		split.Key:  "Die Genauigkeitsangaben beziehen sich auf den stationären Durchfluss.",
		tagged.Key: strings.Replace(tagged.RichText, "The F7", "Der F7", 1),
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	rebuilt := string(out)
	for _, want := range []string{
		"Nachtrag zur Gebrauchsanweisung",
		"Die Genauigkeitsangaben beziehen sich auf den stationären Durchfluss.",
		"Der F7<sup>m</sup> Flow Sensor",
	} {
		if !strings.Contains(rebuilt, want) {
			t.Errorf("rebuilt document is missing %q", want)
		}
	}

	// Structure has to survive: the rebuilt file must still parse, and only
	// the translated segments may have changed.
	after, err := XML{}.Parse(out)
	if err != nil {
		t.Fatalf("re-parse rebuilt document: %v", err)
	}
	if len(after) != len(segments) {
		t.Fatalf("segment count changed after round trip: %d → %d", len(segments), len(after))
	}

	changed := 0
	for i := range segments {
		if segments[i].Text != after[i].Text {
			changed++
		}
	}
	if changed != len(translations) {
		t.Errorf("%d segments changed, want exactly %d", changed, len(translations))
	}
}

func mustSegment(t *testing.T, byText map[string]common.Segment, text string) common.Segment {
	t.Helper()
	s, ok := byText[text]
	if !ok {
		t.Fatalf("fixture segment not found: %q", text)
	}
	return s
}

// TestXMLReconstructTranslatesEverySegment exercises the single-pass forward
// assembly with every range populated at once. A range-ordering or
// position-tracking bug shows up here as dropped or duplicated content, which
// a partial translation map can easily hide.
func TestXMLReconstructTranslatesEverySegment(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Zero-padded so no marker is a prefix of another — "DE1" would otherwise
	// match inside "DE10" and make the occurrence counts meaningless.
	marker := func(i int) string { return fmt.Sprintf("DE%03d", i) }

	translations := make(map[string]string, len(segments))
	for i, s := range segments {
		// Rich text where present, so inline chips round-trip too.
		if s.RichText != "" {
			translations[s.Key] = s.RichText
			continue
		}
		translations[s.Key] = marker(i)
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	rebuilt := string(out)
	if _, err := common.ParseXMLDoc(out); err != nil {
		t.Fatalf("rebuilt document is not well-formed: %v", err)
	}

	// Each marker must appear exactly once: zero means the range was dropped,
	// more than one means it was written twice. Those are precisely the
	// failure modes a forward-assembly or range-ordering bug produces.
	// How many cross-references point at each defining segment. Its translation
	// is written into every one of them, so those markers appear
	// more than once by design.
	refCounts := map[string]int{}
	for _, s := range segments {
		for _, key := range refKeysIn(s.RichText) {
			refCounts[key]++
		}
	}

	for i, s := range segments {
		if s.RichText != "" {
			continue // chip-bearing segments keep their original words
		}
		want := 1 + refCounts[s.Key]
		if got := strings.Count(rebuilt, marker(i)); got != want {
			t.Errorf("marker %s (segment %q) appears %d times, want %d (1 + %d references)",
				marker(i), s.Text, got, want, refCounts[s.Key])
		}
	}

	// Structure outside the segments is untouched: the settings comment, the
	// DOCTYPE and the file-hash all survive a full-document rewrite.
	for _, marker := range []string{
		`<!DOCTYPE tms SYSTEM "Tms.dtd"[]>`,
		`file-hash="0F1E2D3C4B5A69788796A5B4C3D2E1F0"`,
		"<!--TMS Settings",
		`xml:space="preserve"`,
	} {
		if !strings.Contains(rebuilt, marker) {
			t.Errorf("rebuilt document lost %q", marker)
		}
	}
}

// TestXMLMetaRefKeepsAttributes is the ST4-specific guard. <meta-ref> points at
// a CMS node via linkid; losing that attribute on export silently breaks the
// cross-reference in the delivered document.
func TestXMLMetaRefKeepsAttributes(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var target common.Segment
	for _, s := range segments {
		if strings.Contains(s.RichText, "x-xml-meta-ref") {
			target = s
			break
		}
	}
	if target.Key == "" {
		t.Fatal("no segment with a meta-ref chip found in the fixture")
	}

	out, err := XML{}.Reconstruct(original, map[string]string{
		target.Key: strings.Replace(target.RichText, "the pressure correction", "die Druckkorrektur", 1),
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	rebuilt := string(out)
	if !strings.Contains(rebuilt, "die Druckkorrektur") {
		t.Error("translation was not applied")
	}
	// Both referenced nodes must still be wired up.
	for _, linkid := range []string{"datanode_1001_200000000001", "datanode_1001_200000000002"} {
		if strings.Count(rebuilt, linkid) != strings.Count(string(original), linkid) {
			t.Errorf("linkid %s occurrence count changed: %d → %d",
				linkid, strings.Count(string(original), linkid), strings.Count(rebuilt, linkid))
		}
	}
}

// TestXMLChipReorderingRestoresTags covers the case German makes routine:
// the translator moves a chip, and the tag has to follow it.
func TestXMLChipReorderingRestoresTags(t *testing.T) {
	src := []byte(`<doc><p>Set <b>Eco</b> or <i>Boost</i> mode.</p></doc>`)

	segments, err := XML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	if got, want := segments[0].RichText, `Set <g id="1" ctype="bold">Eco</g> or <g id="2" ctype="italic">Boost</g> mode.`; got != want {
		t.Fatalf("rich text = %q, want %q", got, want)
	}

	// Swap the chips, as a translator reordering the clause would.
	out, err := XML{}.Reconstruct(src, map[string]string{
		segments[0].Key: `<g id="2" ctype="italic">Turbomodus</g> oder <g id="1" ctype="bold">Sparmodus</g> einstellen.`,
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	want := `<doc><p><i>Turbomodus</i> oder <b>Sparmodus</b> einstellen.</p></doc>`
	if string(out) != want {
		t.Errorf("reconstruct =\n %s\nwant\n %s", out, want)
	}
}

// TestXMLDroppedChipDegradesCleanly — a translator deleting a chip must not
// produce malformed XML. The missing-tags QA check is what flags the loss.
func TestXMLDroppedChipDegradesCleanly(t *testing.T) {
	src := []byte(`<doc><p>Set <b>Eco</b> mode.</p></doc>`)

	segments, err := XML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := XML{}.Reconstruct(src, map[string]string{
		segments[0].Key: "Sparmodus einstellen.",
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if want := `<doc><p>Sparmodus einstellen.</p></doc>`; string(out) != want {
		t.Errorf("reconstruct = %s, want %s", out, want)
	}
	if _, err := common.ParseXMLDoc(out); err != nil {
		t.Errorf("rebuilt document is not well-formed: %v", err)
	}
}

// TestXMLEscapesTranslatedText guards against a translation containing markup
// characters breaking the document.
func TestXMLEscapesTranslatedText(t *testing.T) {
	src := []byte(`<doc><p>Less than five seconds</p></doc>`)

	segments, err := XML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := XML{}.Reconstruct(src, map[string]string{
		segments[0].Key: "<5 Sekunden & mehr",
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if want := `<doc><p>&lt;5 Sekunden &amp; mehr</p></doc>`; string(out) != want {
		t.Errorf("reconstruct = %s, want %s", out, want)
	}
	if _, err := common.ParseXMLDoc(out); err != nil {
		t.Errorf("rebuilt document is not well-formed: %v", err)
	}
}

// TestXMLDecodesEntitiesInSourceText — the fixture has "&lt;5 seconds" and
// "&gt;80 rpm" cells. A translator must see "<5 seconds", never the raw
// entity, or they'd translate the escaping along with the text.
func TestXMLDecodesEntitiesInSourceText(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	decoded := 0
	for _, s := range segments {
		for _, entity := range []string{"&lt;", "&gt;", "&amp;", "&quot;"} {
			if strings.Contains(s.Text, entity) {
				t.Errorf("segment %q still contains the raw entity %q", s.Text, entity)
			}
		}
		if strings.HasPrefix(s.Text, "<") || strings.HasPrefix(s.Text, ">") {
			decoded++
		}
	}
	if decoded == 0 {
		t.Error("no segment starts with a decoded < or >, expected the fixture's comparison cells")
	}
}

// TestXMLSkipsNonTranslatable honours the opt-out conventions the HTML and
// Android parsers already respect.
func TestXMLSkipsNonTranslatable(t *testing.T) {
	src := []byte(`<doc><p>Translate this</p><p translate="no">Leave this</p><p translatable="false">And this</p></doc>`)

	segments, err := XML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(segments), segments)
	}
	if segments[0].Text != "Translate this" {
		t.Errorf("segment text = %q, want %q", segments[0].Text, "Translate this")
	}
}

// TestXMLSegmentContext surfaces the enclosing ST4 node title, which is what
// tells a translator which table an isolated "Range" cell belongs to.
func TestXMLSegmentContext(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, s := range segments {
		if s.Text == "Nominal flow rate" {
			if want := "Revised Information: F7m Flow Sensor Specifications"; s.Context != want {
				t.Errorf("context = %q, want %q", s.Context, want)
			}
			return
		}
	}
	t.Fatal(`segment "Nominal flow rate" not found`)
}

// TestXMLKeysAreUniqueAndStable — keys address segments across the whole
// pipeline; duplicates would make translations overwrite each other, and
// instability would break export.
func TestXMLKeysAreUniqueAndStable(t *testing.T) {
	content := readFixture(t, st4SourcePath)

	first, err := XML{}.Parse(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	seen := map[string]int{}
	for i, s := range first {
		if prev, dup := seen[s.Key]; dup {
			t.Fatalf("duplicate key %q at segments %d and %d (%q / %q)",
				s.Key, prev, i, first[prev].Text, s.Text)
		}
		seen[s.Key] = i
	}

	second, err := XML{}.Parse(content)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	for i := range first {
		if first[i].Key != second[i].Key {
			t.Fatalf("key at %d not stable across parses: %q vs %q", i, first[i].Key, second[i].Key)
		}
	}
}

// TestXMLChipCarriesOriginalMarkup — an ST4 <meta-ref> is only identifiable by
// its linkid, which the ctype can't express. The chip has to carry the whole
// start tag so the editor can show it on hover.
func TestXMLChipCarriesOriginalMarkup(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var metaRef common.Segment
	for _, s := range segments {
		if strings.Contains(s.RichText, "x-xml-meta-ref") {
			metaRef = s
			break
		}
	}
	if metaRef.Key == "" {
		t.Fatal("no meta-ref chip found in the fixture")
	}

	if !strings.Contains(metaRef.RichText, "original_markup=") {
		t.Fatalf("meta-ref chip carries no original_markup: %q", metaRef.RichText)
	}
	// The escaped value must decode to the element exactly as written in the
	// source, linkid included.
	for _, want := range []string{
		"&lt;meta-ref",
		"linkid=&quot;datanode_1001_200000000001&quot;",
	} {
		if !strings.Contains(metaRef.RichText, want) {
			t.Errorf("original_markup missing %q:\n%s", want, metaRef.RichText)
		}
	}

	// Formatting tags with no attributes stay lean.
	var sup common.Segment
	for _, s := range segments {
		if strings.Contains(s.RichText, "x-html-sup") {
			sup = s
			break
		}
	}
	if sup.Key == "" {
		t.Fatal("no x-html-sup chip found in the fixture")
	}
	if strings.Contains(sup.RichText, "original_markup=") {
		t.Errorf("a bare <sup> chip should not carry original_markup: %q", sup.RichText)
	}
}

// --- Cross-references ---

// TestXMLCrossReferenceIsAPlaceholder is the load-bearing test for the model
// change: an ST4 <meta-ref> renders text generated from another node, so that
// text must not enter the segment. Keeping it out is what lets our source align
// with a TM built by a tool that models the same construct as a placeholder.
func TestXMLCrossReferenceIsAPlaceholder(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var seg common.Segment
	for _, s := range segments {
		if strings.HasPrefix(s.Text, "For H") && strings.Contains(s.Text, "pressure correction") {
			seg = s
			break
		}
	}
	if seg.Key == "" {
		t.Fatal("pressure-correction segment not found")
	}

	// The negative assertion is the point — "Eco"/"Boost" are translated once at
	// their defining node, not inside every reference to it.
	for _, generated := range []string{"Eco", "Boost"} {
		if strings.Contains(seg.Text, generated) {
			t.Errorf("cross-reference text %q leaked into the segment: %q", generated, seg.Text)
		}
	}
	// One self-closing chip per reference, not a pair.
	if got := strings.Count(seg.RichText, `ctype="x-xml-meta-ref"`); got != 2 {
		t.Errorf("want 2 meta-ref chips, got %d: %q", got, seg.RichText)
	}
	if strings.Contains(seg.RichText, `<g id="2"`) || strings.Contains(seg.RichText, "</g>") &&
		!strings.Contains(seg.RichText, "x-html-sub") {
		t.Errorf("meta-ref should be self-closing, not paired: %q", seg.RichText)
	}

	// A non-reference inline element is untouched: <sub> keeps its "2".
	if !strings.Contains(seg.Text, "H2O") {
		t.Errorf("<sub> content should still be translatable text: %q", seg.Text)
	}
}

// TestXMLCrossReferenceAlignsWithTMSource — the reason for the change. Our
// parsed source must line up with what a TMX built from the same document
// carries, or every cross-reference segment scores as a fuzzy match instead of
// an exact one.
func TestXMLCrossReferenceAlignsWithTMSource(t *testing.T) {
	// The shape a TMX from the reference's CAT tool gives this sentence: the
	// double space is the placeholder contributing no text, exactly as we do.
	const tmSourceText = "For H2O the pressure correction can be set to  or ."

	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var ours string
	for _, s := range segments {
		if strings.HasPrefix(s.Text, "For H") && strings.Contains(s.Text, "pressure correction") {
			ours = s.Text
			break
		}
	}

	// The two sources differ by one comma (a different source revision), which
	// is ordinary fuzziness. Everything else — crucially the placeholder gaps —
	// must be identical.
	if normalized := strings.Replace(ours, "H2O,", "H2O", 1); normalized != tmSourceText {
		t.Errorf("source does not align with the TM entry:\n ours: %q\n  TM:  %q", normalized, tmSourceText)
	}
}

// TestXMLCrossReferenceWordCount — a cross-reference is not translated, so it
// must not be billed. This is the commercial half of the change.
func TestXMLCrossReferenceWordCount(t *testing.T) {
	const withRefs = `<doc><p>Set it to <meta-ref linkid="a">Eco</meta-ref> or <meta-ref linkid="b">Boost</meta-ref>.</p></doc>`

	segments, err := XML{}.Parse([]byte(withRefs))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	withRefsCount := wordcount.CountWords(segments[0].Text)

	// The same sentence with the reference text authored inline. The delta is
	// the words that are not billed.
	inline, err := XML{}.Parse([]byte(`<doc><p>Set it to Eco or Boost.</p></doc>`))
	if err != nil {
		t.Fatalf("parse inline: %v", err)
	}
	inlineCount := wordcount.CountWords(inline[0].Text)

	if withRefsCount >= inlineCount {
		t.Errorf("cross-references are still billed: %d words with refs vs %d inline (%q)",
			withRefsCount, inlineCount, segments[0].Text)
	}
	if want := inlineCount - 2; withRefsCount != want {
		t.Errorf("word count = %d, want %d (two reference words excluded)", withRefsCount, want)
	}
}

// TestXMLCrossReferenceExportsVerbatim — the reference must round-trip
// untouched, content included, so the CMS can resolve it on publish. Restoring
// only the start and end tags would silently empty every cross-reference in the
// delivered document.
func TestXMLCrossReferenceExportsVerbatim(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var seg common.Segment
	for _, s := range segments {
		if strings.Contains(s.RichText, "x-xml-meta-ref") {
			seg = s
			break
		}
	}
	if seg.Key == "" {
		t.Fatal("no cross-reference segment found")
	}

	out, err := XML{}.Reconstruct(original, map[string]string{
		seg.Key: strings.Replace(seg.RichText, "the pressure correction", "die Druckkorrektur", 1),
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	rebuilt := string(out)
	if !strings.Contains(rebuilt, "die Druckkorrektur") {
		t.Error("translation was not applied")
	}
	// Whole element, attributes AND generated content.
	want := `<meta-ref type="static" name="custom.acme.device.ui.title" linkid="datanode_1001_200000000001">Eco</meta-ref>`
	if !strings.Contains(rebuilt, want) {
		t.Errorf("cross-reference did not round-trip verbatim; want to find:\n%s", want)
	}
}

// st4DeliveredPath is a German ST4 delivery of the same source, produced with
// the reference's CAT tool. It is the authority on what our export has to look
// like.
const st4DeliveredPath = "testdata/st4_delivered_de.xml"

// TestXMLCrossReferenceExportsTranslatedTerm pins that an exported
// cross-reference carries the translated term. ST4 does not fill the content
// at publish: the delivered file carries `<meta-ref …>Sparmodus</meta-ref>`,
// translated. Exporting the source content would ship a German manual reading
// "Eco".
//
// The expected string is lifted from the delivered file, not written by hand.
func TestXMLCrossReferenceExportsTranslatedTerm(t *testing.T) {
	original := readFixture(t, st4SourcePath)
	delivered := string(readFixture(t, st4DeliveredPath))

	// The exact element the delivered file carries.
	const wantElement = `<meta-ref type="static" name="custom.acme.device.ui.title" linkid="datanode_1001_200000000001">Sparmodus</meta-ref>`
	if !strings.Contains(delivered, wantElement) {
		t.Fatalf("fixture drift: the delivered file no longer contains\n%s", wantElement)
	}

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Translate the DEFINING node — the standalone <meta> whose value every
	// reference to it renders.
	translations := map[string]string{}
	var definingFound bool
	for _, s := range segments {
		if s.Text == "Eco" && strings.Contains(s.Key, "metalist/meta") {
			translations[s.Key] = "Sparmodus"
			definingFound = true
		}
		// EVERY segment that references it — a reference is only re-emitted
		// when its own segment is translated and therefore spliced.
		if strings.Contains(s.RichText, "datanode_1001_200000000001") {
			translations[s.Key] = s.RichText
		}
	}
	if !definingFound {
		t.Fatal(`defining <meta> segment for "Eco" not found`)
	}
	if len(translations) < 2 {
		t.Fatal("no segment references datanode_1001_200000000001")
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	if !strings.Contains(string(out), wantElement) {
		t.Errorf("export did not resolve the reference to its translation.\nwant to find: %s", wantElement)
	}
	if strings.Contains(string(out), `linkid="datanode_1001_200000000001">Eco</meta-ref>`) {
		t.Error("export kept the SOURCE term inside a translated reference")
	}
}

// TestXMLCrossReferenceCannotBeLost pins the property the delivered file's
// workflow lacks: two of the four references are simply missing from the delivered file,
// because a translator deleted the tags. Ours are placeholders resolved at
// export, so the count is structural — no edit can drop one.
func TestXMLCrossReferenceCannotBeLost(t *testing.T) {
	original := readFixture(t, st4SourcePath)
	wantRefs := strings.Count(string(original), "<meta-ref")

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Translate every segment, however crudely — the references must survive.
	translations := make(map[string]string, len(segments))
	for i, s := range segments {
		if s.RichText != "" {
			translations[s.Key] = s.RichText
			continue
		}
		translations[s.Key] = fmt.Sprintf("DE%03d", i)
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if got := strings.Count(string(out), "<meta-ref"); got != wantRefs {
		t.Errorf("cross-reference count changed on export: %d → %d (the delivered file lost 2 of 4 this way)",
			wantRefs, got)
	}
}

// refKeysIn extracts the ref_key attributes from a segment's rich text — the
// defining segments whose translation this segment renders.
func refKeysIn(richText string) []string {
	var out []string
	for _, m := range refKeyAttrRE.FindAllStringSubmatch(richText, -1) {
		decoded, _ := common.XMLUnescape(m[1])
		out = append(out, decoded)
	}
	return out
}

var refKeyAttrRE = regexp.MustCompile(`ref_key="([^"]*)"`)

// TestXMLCrossReferenceResolution — the linkid→segment mapping is exact, not a
// guess: the reference names the node id (linkid tail) and the attribute name,
// and the defining <meta> carries both.
func TestXMLCrossReferenceResolution(t *testing.T) {
	segments, err := XML{}.Parse(readFixture(t, st4SourcePath))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Every reference must name a key that is itself a real segment.
	keys := map[string]string{}
	for _, s := range segments {
		keys[s.Key] = s.Text
	}

	found := 0
	for _, s := range segments {
		for _, key := range refKeysIn(s.RichText) {
			found++
			term, ok := keys[key]
			if !ok {
				t.Errorf("segment %q references %q, which is not a segment", s.Key, key)
				continue
			}
			if term != "Eco" && term != "Boost" {
				t.Errorf("reference resolved to %q, want the defining term", term)
			}
		}
	}
	if found != 4 {
		t.Errorf("resolved %d references, want 4 (the fixture's meta-ref count)", found)
	}
}

// TestXMLCrossReferenceUnresolvableIsInert — a linkid pointing outside this
// document must degrade to today's behaviour rather than breaking the parse or
// emptying the element.
func TestXMLCrossReferenceUnresolvableIsInert(t *testing.T) {
	src := []byte(`<doc><p>See <meta-ref linkid="datanode_1_999999" name="x">Elsewhere</meta-ref> now.</p></doc>`)

	segments, err := XML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	if strings.Contains(segments[0].RichText, "ref_key") {
		t.Errorf("unresolvable linkid should not emit ref_key: %q", segments[0].RichText)
	}

	// Still a placeholder, and still round-trips with its content.
	out, err := XML{}.Reconstruct(src, map[string]string{segments[0].Key: segments[0].RichText})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !strings.Contains(string(out), `>Elsewhere</meta-ref>`) {
		t.Errorf("unresolvable reference lost its content: %s", out)
	}
}

// TestXMLCrossReferenceFallsBackWhenTermUntranslated — a reference whose
// defining node hasn't been translated yet keeps the source term rather than
// emptying, so a partially translated file is never worse than the source.
func TestXMLCrossReferenceFallsBackWhenTermUntranslated(t *testing.T) {
	original := readFixture(t, st4SourcePath)

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Translate ONLY the referencing sentence, not the defining <meta>.
	translations := map[string]string{}
	for _, s := range segments {
		if strings.Contains(s.RichText, "datanode_1001_200000000001") {
			translations[s.Key] = s.RichText
		}
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !strings.Contains(string(out), `linkid="datanode_1001_200000000001">Eco</meta-ref>`) {
		t.Errorf("untranslated term should leave the source content in place:\n%s",
			firstMetaRef(string(out)))
	}
}

func firstMetaRef(doc string) string {
	i := strings.Index(doc, "<meta-ref")
	if i < 0 {
		return "(no meta-ref found)"
	}
	j := strings.Index(doc[i:], "</meta-ref>")
	if j < 0 {
		return doc[i:min(i+160, len(doc))]
	}
	return doc[i : i+j+len("</meta-ref>")]
}

// TestXMLExportMatchesDeliveredFile compares our cross-reference output against
// the delivered German file.
//
// Two claims, both checked against the real artefact rather than expectations
// written from memory:
//
//  1. Where the delivered file got it right, we produce the identical element —
//     same attributes, same translated content.
//  2. Where it lost a reference, we keep it. The delivered file contains
//     2 of the source's 4; the pressure-correction sentence reads
//     "…auf  oder  eingestellt werden." with the references gone, because a
//     translator deleted the tags. Ours can't be deleted, so all 4 survive.
func TestXMLExportMatchesDeliveredFile(t *testing.T) {
	original := readFixture(t, st4SourcePath)
	delivered := string(readFixture(t, st4DeliveredPath))

	segments, err := XML{}.Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Translate the two defining terms exactly as the delivered file does, and mark
	// the reference-bearing sentences translated so their ranges are spliced.
	translations := map[string]string{}
	for _, s := range segments {
		switch s.Text {
		case "Eco":
			translations[s.Key] = "Sparmodus"
		case "Boost":
			translations[s.Key] = "Turbomodus"
		}
		if strings.Contains(s.RichText, "meta-ref") {
			translations[s.Key] = s.RichText
		}
	}

	out, err := XML{}.Reconstruct(original, translations)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	ours := string(out)

	// 1. Every reference in the delivered file must appear in our output too.
	deliveredRefs := metaRefElements(delivered)
	if len(deliveredRefs) == 0 {
		t.Fatal("fixture drift: the delivered file has no cross-references")
	}
	for _, ref := range deliveredRefs {
		if !strings.Contains(ours, ref) {
			t.Errorf("our export differs from the delivered file.\nmissing: %s", ref)
		}
	}

	// 2. …and we keep the ones the delivered file dropped.
	sourceCount := strings.Count(string(original), "<meta-ref")
	oursCount := len(metaRefElements(ours))
	if oursCount != sourceCount {
		t.Errorf("kept %d of %d references, want all of them", oursCount, sourceCount)
	}
	if oursCount <= len(deliveredRefs) {
		t.Errorf("expected to preserve MORE references than the delivered file (%d) — got %d",
			len(deliveredRefs), oursCount)
	}
	// No reference may still carry an untranslated term.
	for _, ref := range metaRefElements(ours) {
		if strings.HasSuffix(ref, ">Eco</meta-ref>") || strings.HasSuffix(ref, ">Boost</meta-ref>") {
			t.Errorf("reference left untranslated: %s", ref)
		}
	}
}

// metaRefElements returns every complete <meta-ref>…</meta-ref> in a document.
func metaRefElements(doc string) []string {
	var out []string
	for _, part := range strings.Split(doc, "<meta-ref")[1:] {
		if i := strings.Index(part, "</meta-ref>"); i >= 0 {
			out = append(out, "<meta-ref"+part[:i]+"</meta-ref>")
		}
	}
	return out
}
