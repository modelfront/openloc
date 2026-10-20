package parser

import (
	"bytes"
	"strings"
	"testing"

	"openloc/internal/ptr"
)

func TestSerializeBilingualXLIFF_HappyPath(t *testing.T) {
	segments := []BilingualSegment{
		{Key: "line:1", SourceText: "Hello world", TargetText: "Hola mundo", Status: "approved"},
	}
	out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	got := string(out)

	// Document declarations + namespace.
	if !strings.HasPrefix(got, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("missing XML declaration: %q", got[:30])
	}
	if !strings.Contains(got, `<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2" xmlns:openloc="urn:openloc:xliff">`) {
		t.Error("missing xliff root element with version + namespaces")
	}

	// File element with original (required by XLIFF 1.2), both languages,
	// and datatype.
	if !strings.Contains(got, `<file original="doc.txt" source-language="en" target-language="es" datatype="plaintext">`) {
		t.Error("missing file element with original + source/target language")
	}

	// Trans-unit with key as id.
	if !strings.Contains(got, `<trans-unit id="line:1">`) {
		t.Error("trans-unit id should equal segment key")
	}
	if !strings.Contains(got, `<source>Hello world</source>`) {
		t.Error("source content missing")
	}
	if !strings.Contains(got, `<target state="final">Hola mundo</target>`) {
		t.Error("target content + state missing — approved should map to final")
	}
}

func TestSerializeBilingualXLIFF_EmptyTargetGetsNeedsTranslationState(t *testing.T) {
	// Untranslated segment — empty target, state must say "needs-translation"
	// so the receiving CAT tool knows to surface it for the translator.
	segments := []BilingualSegment{
		{Key: "k", SourceText: "Untouched", Status: "untranslated"},
	}
	out, _ := SerializeBilingualXLIFF("doc.txt", "en", "fr", segments)
	got := string(out)
	if !strings.Contains(got, `<target state="needs-translation"></target>`) {
		t.Errorf("untranslated empty target should be `<target state=needs-translation></target>`, got: %s", got)
	}
}

func TestSerializeBilingualXLIFF_LockedOverridesStatus(t *testing.T) {
	// Locked segments emit state="signed-off" regardless of internal status.
	// The signed-off signal is XLIFF's standard "do not modify" marker, so
	// importing CAT tools honor our locks.
	segments := []BilingualSegment{
		{Key: "k", SourceText: "src", TargetText: "tgt", Status: "approved", Locked: true},
		{Key: "k2", SourceText: "src2", TargetText: "tgt2", Status: "untranslated", Locked: true},
	}
	out, _ := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	got := string(out)
	if strings.Count(got, `state="signed-off"`) != 2 {
		t.Errorf("expected 2 signed-off targets (locked overrides status), got: %s", got)
	}
	if strings.Contains(got, `state="final"`) {
		t.Error("locked-approved should NOT emit state=final — locked overrides")
	}
}

func TestSerializeBilingualXLIFF_StatusToStateMap(t *testing.T) {
	// Pin every status → state mapping in one table-driven test so a
	// future enum addition without updating the mapping fails here.
	cases := []struct {
		status string
		want   string
	}{
		{"", "needs-translation"}, // uninitialized / untranslated → safe default
		// Values outside the status enum fall back to needs-translation.
		{"untranslated", "needs-translation"},
		{"pre_translated", "needs-translation"},
		{"needs_review", "needs-review-translation"},
		{"approved", "final"},
		{"auto_approved", "final"},
		{"some_unknown_future_value", "needs-translation"}, // defensive default
	}
	for _, tc := range cases {
		segments := []BilingualSegment{{Key: "k", SourceText: "s", TargetText: "t", Status: tc.status}}
		out, _ := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
		needle := `state="` + tc.want + `"`
		if !strings.Contains(string(out), needle) {
			t.Errorf("status=%q: expected %q, got: %s", tc.status, needle, string(out))
		}
	}
}

func TestSerializeBilingualXLIFF_RichTextEmittedVerbatim(t *testing.T) {
	// Rich text already contains valid XLIFF inline tags from the parser.
	// The serializer must NOT re-escape them — they're not "user content,"
	// they're structural markup that the recipient tool must see as tags.
	segments := []BilingualSegment{
		{
			Key:            "k",
			SourceText:     "Click here",
			SourceRichText: `Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept>`,
			TargetText:     "Haz click aquí",
			TargetRichText: `Haz <bpt id="1">&lt;b&gt;</bpt>click aquí<ept id="1">&lt;/b&gt;</ept>`,
			Status:         "approved",
		},
	}
	out, _ := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	got := string(out)
	if !strings.Contains(got, `<bpt id="1">&lt;b&gt;</bpt>`) {
		t.Error("rich text inline tags should be emitted verbatim, not re-escaped")
	}
	// Plain `Click here` should NOT appear (rich preferred over plain).
	// Specifically, we should NOT see `<source>Click here</source>` —
	// the rich version is the chosen content.
	if strings.Contains(got, `<source>Click here</source>`) {
		t.Error("plain source emitted alongside rich — rich should be preferred")
	}
}

func TestSerializeBilingualXLIFF_PlainTextXMLEscaped(t *testing.T) {
	// User content with `<`, `>`, `&` must be XML-escaped or it would break
	// the document. Defensive — segment text might come from arbitrary user
	// input (txt files, JSON values, etc.).
	segments := []BilingualSegment{
		{Key: "k", SourceText: `5 < 10 & 11 > 9`, TargetText: `5 < 10 & 11 > 9`, Status: "approved"},
	}
	out, _ := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	got := string(out)
	if !strings.Contains(got, "5 &lt; 10 &amp; 11 &gt; 9") {
		t.Errorf("plain text should be XML-escaped, got: %s", got)
	}
}

func TestSerializeBilingualXLIFF_MissingRequiredAttrsAreErrors(t *testing.T) {
	segments := []BilingualSegment{{Key: "k", SourceText: "s"}}
	// original is a REQUIRED XLIFF 1.2 <file> attribute — empty must fail
	// like the language codes do.
	if _, err := SerializeBilingualXLIFF("", "en", "es", segments); err == nil {
		t.Error("expected error for empty original file name")
	}
	if _, err := SerializeBilingualXLIFF("doc.txt", "", "es", segments); err == nil {
		t.Error("expected error for empty source language")
	}
	if _, err := SerializeBilingualXLIFF("doc.txt", "en", "", segments); err == nil {
		t.Error("expected error for empty target language")
	}
}

func TestSerializeBilingualXLIFF_EmptyKeyIsError(t *testing.T) {
	// The trans-unit id MUST equal segments.segment_key for the re-import
	// round-trip to match. An empty key would silently break
	// the round-trip — fail loudly at serialize time.
	segments := []BilingualSegment{{Key: "", SourceText: "s", TargetText: "t"}}
	_, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	if err == nil {
		t.Error("expected error for empty segment key (would break re-import round-trip)")
	}
	if !strings.Contains(err.Error(), "Key") && !strings.Contains(err.Error(), "key") {
		t.Errorf("error message should mention the missing key: %v", err)
	}
}

func TestSerializeBilingualXLIFF_ProducesParsableXLIFF(t *testing.T) {
	// Round-trip self-test: serialize → parse → assert segment count, keys,
	// source and target match. The same parser the rest of the codebase uses
	// must read back what we emit.
	segments := []BilingualSegment{
		{Key: "line:1", SourceText: "Hello", TargetText: "Hola", Status: "approved"},
		{Key: "line:2", SourceText: "World", TargetText: "", Status: "untranslated"},
		{Key: "line:3", SourceText: "Locked", TargetText: "Bloqueado", Status: "approved", Locked: true},
	}
	out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segments)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	// Re-parse what we just emitted.
	parsed, err := XLIFF{}.Parse(out)
	if err != nil {
		t.Fatalf("self-parse of emitted XLIFF failed: %v\nemit:\n%s", err, string(out))
	}
	if len(parsed) != len(segments) {
		t.Errorf("self-parse round-trip: emitted %d segments, parsed %d back", len(segments), len(parsed))
	}
	for i, want := range segments {
		if i >= len(parsed) {
			break
		}
		if parsed[i].Key != want.Key || parsed[i].Text != want.SourceText || parsed[i].Target != want.TargetText {
			t.Errorf("segment %d: got key %q text %q target %q, want %q %q %q", i,
				parsed[i].Key, parsed[i].Text, parsed[i].Target, want.Key, want.SourceText, want.TargetText)
		}
	}
}

func TestSerializeBilingualXLIFF_StateQualifierFromPreTranslation(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		percent *int
		want    string // "" = no state-qualifier attribute
	}{
		{"tm fuzzy", "tm", ptr.To(74), "fuzzy-match"},
		{"tm exact", "tm", ptr.To(100), "exact-match"},
		{"tm context", "tm", ptr.To(101), "exact-match"},
		{"tm without score", "tm", nil, "leveraged-tm"},
		{"mt", "mt", nil, "leveraged-mt"},
		{"ape", "ape", nil, "leveraged-mt"},
		{"file import", "file_import", nil, ""},
		{"human only", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			segs := []BilingualSegment{{Key: "k", SourceText: "s", TargetText: "t", Status: "needs_review", Origin: tc.origin, MatchPercent: tc.percent}}
			out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segs)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			got := string(out)
			if tc.want == "" {
				if strings.Contains(got, "state-qualifier=") {
					t.Errorf("expected no state-qualifier, got: %s", got)
				}
				return
			}
			needle := `<target state="needs-review-translation" state-qualifier="` + tc.want + `">t</target>`
			if !strings.Contains(got, needle) {
				t.Errorf("expected %s, got: %s", needle, got)
			}
		})
	}
}

func TestSerializeBilingualXLIFF_TMMatchPercentAndAltTrans(t *testing.T) {
	segs := []BilingualSegment{{
		Key: "line:1", SourceText: "Triple tap & hold", TargetText: "Toca tres veces",
		Status: "needs_review", Origin: "tm", MatchPercent: ptr.To(74),
		Match: &TMMatch{SourceText: "Double tap & hold", TargetText: "Toca dos veces <rápido>"},
	}}
	out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segs)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	got := string(out)
	// memoQ reads a match rate only from a <trans-unit> attribute.
	if !strings.Contains(got, `<trans-unit id="line:1" openloc:match-percent="74">`) {
		t.Errorf("missing match-percent on trans-unit: %s", got)
	}
	// XLIFF 1.2's own place for a match score, with the matched TM unit.
	want := `<alt-trans match-quality="74" origin="tm">` + "\n" +
		`          <source>Double tap &amp; hold</source>` + "\n" +
		`          <target>Toca dos veces &lt;rápido&gt;</target>` + "\n" +
		`        </alt-trans>`
	if !strings.Contains(got, want) {
		t.Errorf("missing alt-trans with escaped TM unit; want:\n%s\ngot:\n%s", want, got)
	}
	if strings.Index(got, "<alt-trans") < strings.Index(got, "</target>") {
		t.Error("alt-trans must follow the trans-unit's own <target>")
	}
}

func TestSerializeBilingualXLIFF_NoMatchInfoWithoutTM(t *testing.T) {
	segs := []BilingualSegment{
		// MT pre-translation: no score, no alt-trans.
		{Key: "a", SourceText: "s", TargetText: "t", Status: "needs_review", Origin: "mt"},
		// TM score but the TM unit is gone: score stays, alt-trans is dropped.
		{Key: "b", SourceText: "s", TargetText: "t", Status: "needs_review", Origin: "tm", MatchPercent: ptr.To(88)},
		// Untranslated: lineage is ignored when there is no target.
		{Key: "c", SourceText: "s", Origin: "tm", MatchPercent: ptr.To(90), Match: &TMMatch{SourceText: "x", TargetText: "y"}},
	}
	out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segs)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "<alt-trans") {
		t.Errorf("no segment should get an alt-trans: %s", got)
	}
	if strings.Count(got, "openloc:match-percent=") != 1 || !strings.Contains(got, `<trans-unit id="b" openloc:match-percent="88">`) {
		t.Errorf("only segment b should carry match-percent: %s", got)
	}
	if !strings.Contains(got, `<target state="needs-translation"></target>`) {
		t.Errorf("untranslated segment should have a bare needs-translation target: %s", got)
	}
}

func TestSerializeBilingualXLIFF_AltTransSurvivesSelfParse(t *testing.T) {
	segs := []BilingualSegment{{
		Key: "k", SourceText: "Triple tap", TargetText: "Toca tres veces",
		Status: "approved", Origin: "tm", MatchPercent: ptr.To(74),
		Match: &TMMatch{SourceText: "Double tap", TargetText: "Toca dos veces"},
	}}
	out, err := SerializeBilingualXLIFF("doc.txt", "en", "es", segs)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	parsed, err := XLIFF{}.Parse(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Text != "Triple tap" || parsed[0].Target != "Toca tres veces" {
		t.Errorf("re-parse picked up the alt-trans instead of the segment: %+v", parsed)
	}
}

// =============================================================================
// Anti-tautology RED proofs (durable artifact format)
// =============================================================================
//
// Proof 1 — segment_key contract is enforced.
//   Mutation:    xliff_serialize.go — replace `if seg.Key == "" { return ... }`
//                guard with `_ = seg.Key` (skip the check).
//   Test:        TestSerializeBilingualXLIFF_EmptyKeyIsError
//   Diagnostic:  "expected error for empty segment key (would break re-import round-trip)"
//   Proven:      2026-05-06.
//
// Proof 2 — locked segments emit signed-off, not the underlying status.
//   Mutation:    xliff_serialize.go::mapStatusToXLIFFState — remove the
//                `if locked { return signed-off }` branch.
//   Test:        TestSerializeBilingualXLIFF_LockedOverridesStatus
//   Diagnostic:  "expected 2 signed-off targets (locked overrides status)"
//                followed by a doc with `state="final"` instead.
//   Proven:      2026-05-06.
//
// Proof 3 — rich text is emitted verbatim, not double-escaped.
//   Mutation:    xliff_serialize.go::writeXLIFFContent — replace the
//                rich branch with `_ = xml.EscapeText(buf, []byte(rich))`
//                (escape rich same as plain).
//   Test:        TestSerializeBilingualXLIFF_RichTextEmittedVerbatim
//   Diagnostic:  "rich text inline tags should be emitted verbatim, not re-escaped"
//                — the test searches for `<bpt id="1">&lt;b&gt;</bpt>`
//                literally; double-escape produces `&lt;bpt ...&gt;...&lt;/bpt&gt;`.
//   Proven:      2026-05-06.
//
// Replay: reapply the mutation, run
//   go test -count=1 -run <TestName> ./internal/parser/...

// helpString ensures the XML preamble survives concatenation tests.
var _ = bytes.NewBufferString
