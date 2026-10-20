package pipeline

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
)

// runWithTimeout runs TransferTags in a goroutine and fails the test if it
// doesn't return within d. A parser that fails to consume a stray tag
// infinite-loops on nested bpt/ept inputs — this guard turns hangs
// into clean test failures instead of timing out the entire CI run.
func runWithTimeout(t *testing.T, d time.Duration, sourceRich, sourcePlain, mtPlain string) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- TransferTags(sourceRich, sourcePlain, mtPlain) }()
	select {
	case res := <-done:
		return res
	case <-time.After(d):
		t.Fatalf("TransferTags did not return within %s — likely infinite loop", d)
		return ""
	}
}

func TestTransferTags_SimpleGTag(t *testing.T) {
	sourceRich := `<g id="1" ctype="bold">Hello</g> world`
	sourcePlain := "Hello world"
	mtPlain := "Hola mundo"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// "Hello" is source word 0 → "Hola" (MT word 0) is wrapped in <g>
	if !containsTag(result, `<g id="1" ctype="bold">`) {
		t.Errorf("expected <g> tag in result, got %q", result)
	}
	if !containsTag(result, `</g>`) {
		t.Errorf("expected </g> in result, got %q", result)
	}
	// Result should contain all MT text
	if !containsAllChars(result, mtPlain) {
		t.Errorf("result should contain all MT text characters, got %q", result)
	}
}

func TestTransferTags_MultipleGTags(t *testing.T) {
	sourceRich := `<g id="1" ctype="bold">Final</g> <g id="2" ctype="bold">Notification</g>`
	sourcePlain := "Final Notification"
	mtPlain := "Notificación Final"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should have two <g> tags
	count := countOccurrences(result, "<g ")
	if count != 2 {
		t.Errorf("expected 2 <g> tags, got %d in %q", count, result)
	}
	// All MT text should be present
	if !containsAllChars(result, mtPlain) {
		t.Errorf("result should contain all MT text, got %q", result)
	}
}

func TestTransferTags_BptEpt(t *testing.T) {
	sourceRich := `Click <bpt id="1"><b></bpt>here<ept id="1"></b></ept> to continue`
	sourcePlain := "Click here to continue"
	mtPlain := "Haga clic aquí para continuar"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !containsTag(result, `<bpt id="1">`) {
		t.Errorf("expected <bpt> tag, got %q", result)
	}
	if !containsTag(result, `<ept id="1">`) {
		t.Errorf("expected <ept> tag, got %q", result)
	}
}

// An engine's tag-mode output holds the segment's characters as they are, so
// a `<` or `>` that is text (Android `&gt;`, a comparison, a typed bracket,
// an uppercase look-alike of a tag) must survive. Chip and formatting markup
// is removed, and the native code inside bpt/ept/it with it, as the XLIFF
// parser does for a segment's plain text.
func TestStripInlineTags_KeepsLiteralAngleBrackets(t *testing.T) {
	cases := map[string]string{
		`Abra <g id="1" ctype="bold">Ajustes</g> > Privacidad`:           "Abra Ajustes > Privacidad",
		`a < b <g id="1" ctype="italic">it</g> c > d`:                    "a < b it c > d",
		`Text <x id="2"/> then <br/> end`:                                "Text  then  end",
		`<bpt id="1">&lt;b&gt;</bpt>bold<ept id="1">&lt;/b&gt;</ept> on`: "bold on",
		`line<it id="3" pos="open">&lt;br/&gt;</it>break`:                "linebreak",
		`A <it id="1"/>x <it id="2">&lt;br/&gt;</it>y`:                   "A x y",
		`<bpt id="1"/>open<bpt id="2">&lt;b&gt;</bpt>bold`:               "openbold",
		`Click <hi mtype="x-bold">here</hi> now`:                         "Click here now",
		`<strong>Go</strong> <em>now</em> <sup>2</sup>`:                  "Go now 2",
		`Press <A> to jump, <g id="1">then</g> <X>`:                      "Press <A> to jump, then <X>",
		`if x < 3 and y > 2`:                                             "if x < 3 and y > 2",
		`no tags at all`:                                                 "no tags at all",
	}
	for in, want := range cases {
		if got := stripInlineTags(in); got != want {
			t.Errorf("stripInlineTags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTransferTags_NoTags(t *testing.T) {
	result := TransferTags("Hello world", "Hello world", "Hola mundo")
	if result != "" {
		t.Errorf("expected empty result for plain text, got %q", result)
	}
}

func TestTransferTags_EmptyInputs(t *testing.T) {
	if result := TransferTags("", "Hello", "Hola"); result != "" {
		t.Errorf("expected empty for empty rich text, got %q", result)
	}
	if result := TransferTags("<g>x</g>", "", "Hola"); result != "" {
		t.Errorf("expected empty for empty source plain, got %q", result)
	}
	if result := TransferTags("<g>x</g>", "x", ""); result != "" {
		t.Errorf("expected empty for empty MT, got %q", result)
	}
}

func TestTransferTags_SelfClosingTag(t *testing.T) {
	sourceRich := `Line one<x id="1" ctype="lb"/> line two`
	sourcePlain := "Line one line two"
	mtPlain := "Línea uno línea dos"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !containsTag(result, `<x id="1" ctype="lb"/>`) {
		t.Errorf("expected <x> tag preserved, got %q", result)
	}
}

func TestTransferTags_NestedGTags(t *testing.T) {
	sourceRich := `<g id="1" ctype="bold"><g id="2" ctype="italic">text</g></g>`
	sourcePlain := "text"
	mtPlain := "texto"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should have nested tags wrapping the MT text
	if !containsTag(result, `<g id="1" ctype="bold">`) {
		t.Errorf("expected outer <g> tag, got %q", result)
	}
	if !containsTag(result, `<g id="2" ctype="italic">`) {
		t.Errorf("expected inner <g> tag, got %q", result)
	}
}

func TestTransferTags_WholeSegmentWrapped(t *testing.T) {
	// Entire segment wrapped in one tag — common for bold headings
	sourceRich := `<g id="1" ctype="bold">Final Notification</g>`
	sourcePlain := "Final Notification"
	mtPlain := "Notificación Final"

	result := TransferTags(sourceRich, sourcePlain, mtPlain)
	want := `<g id="1" ctype="bold">Notificación Final</g>`
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

// Nested <bpt>...<bpt>...<ept>...<ept> hangs parseRuns indefinitely.
// Source from demo_tag.xliff trans-unit id=12 — anchor wrapping a bold span.
func TestTransferTags_NestedBptEpt_Reproduce(t *testing.T) {
	sourceRich := `Visit <bpt id="12"><a href="https://example.com"></bpt>our <bpt id="13"><b></bpt>main<ept id="13"></b></ept> site<ept id="12"></a></ept> for details.`
	sourcePlain := "Visit our main site for details."
	mtPlain := "Visite nuestro sitio principal para detalles."

	result := runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)
	if result == "" {
		t.Fatal("expected non-empty result for tagged input")
	}
	// Both bpt/ept pairs from the source must round-trip.
	for _, want := range []string{`<bpt id="12">`, `<ept id="12">`, `<bpt id="13">`, `<ept id="13">`} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %s in result %q", want, result)
		}
	}
}

// Stray <ept> with no preceding <bpt>: we should not infinite-loop. Best-effort
// behaviour is to drop the stray ept and produce a partial result; the contract
// here is just "terminate".
func TestTransferTags_StrayEpt_DoesNotHang(t *testing.T) {
	sourceRich := `text <ept id="99"></b></ept> more`
	sourcePlain := "text  more"
	mtPlain := "texto más"

	_ = runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)
}

// <bpt> with no closing <ept>: same as above — must terminate, not hang.
func TestTransferTags_UnclosedBpt_DoesNotHang(t *testing.T) {
	sourceRich := `text <bpt id="99"><a></bpt> rest of segment never closed`
	sourcePlain := "text  rest of segment never closed"
	mtPlain := "texto resto del segmento nunca cerrado"

	_ = runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)
}

// --- helpers ---

func containsTag(s, tag string) bool {
	return len(s) > 0 && len(tag) > 0 && countOccurrences(s, tag) > 0
}

func countOccurrences(s, sub string) int {
	count := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}

func containsAllChars(result, plainText string) bool {
	// Check that all non-tag characters from plainText appear in result
	// (tags add extra characters, so result is longer)
	stripped := stripTags(result)
	return stripped == plainText
}

func stripTags(s string) string {
	// Simple tag stripper for test assertions
	result := ""
	inTag := false
	for _, ch := range s {
		if ch == '<' {
			inTag = true
		} else if ch == '>' {
			inTag = false
		} else if !inTag {
			result += string(ch)
		}
	}
	return result
}

// Pins that a paired `<ph id="4">{orderNumber}</ph>` opener never leaks into
// the target as a half tag. Making the slash optional in
// `selfCloseRe` lets the opener match as self-closing and turns this RED.
func TestTransferTags_PairedPhTagDoesNotLeakOpeningOnly(t *testing.T) {
	// Source: paired ph wrapping a placeholder.
	sourceRich := `Your order number is <ph id="4">{orderNumber}</ph>.`
	sourcePlain := `Your order number is {orderNumber}.` // ph content shown after stripping
	mtPlain := `Su número de pedido es {orderNumber}.`

	result := runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)

	// CORE INVARIANT: the result must not have a literal `<ph id=` opener
	// without a matching `</ph>` closer. Either both appear (paired wrap
	// reconstructed) or neither does (clean text). Anything else means the
	// regex matched the opening as a "self-closing" tag and leaked it.
	openCount := strings.Count(result, "<ph ")
	closeCount := strings.Count(result, "</ph>")
	if openCount != closeCount {
		t.Errorf("opening/closing ph tags must balance — got %d openers, %d closers in result %q",
			openCount, closeCount, result)
	}

	// And no half-self-closing leak (the user-visible symptom): the opener
	// without a slash is the worst form because it wraps subsequent text in
	// an unclosed XML element on the chip-render side.
	if strings.Contains(result, `<ph id="4">`) && !strings.Contains(result, `</ph>`) {
		t.Errorf("`<ph id=\"4\">` appears without a matching `</ph>` — half-tag leaked into target. Result: %q", result)
	}
}

// Companion test: self-closing `<ph id="X"/>` (with slash) must still match
// the self-closing alternative.
func TestTransferTags_SelfClosingPhStillWorks(t *testing.T) {
	sourceRich := `Press <ph id="1" x="ENTER"/> to submit.`
	sourcePlain := "Press  to submit."
	mtPlain := "Stiskněte  pro odeslání."

	result := runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)
	if !strings.Contains(result, `<ph id="1" x="ENTER"/>`) {
		t.Errorf("self-closing `<ph .../>` must round-trip into target; got %q", result)
	}
}

// When MT plain length differs from source plain length (every real
// translation, every prefixing fallback engine), a proportional offset rounds
// the opening tag's position DOWN, pulling preceding whitespace + leading
// words INTO the chip.
//
// Invariant: when MT contains the source tag's content verbatim (passthrough
// tokens — brand names, URLs, the mock engine's "[FR] " prefix that doesn't
// translate the wrapped word), the wrap's INSIDE text must equal that content.
//
// Anti-tautology: reverting to `mtStart = sourceOffset * mtLen / sourceLen`
// fails all three sub-tests with insides " bold" / "n italics" / "to
// machinetranslate.org".
func TestTransferTags_OpeningTagDriftsLeftWhenMTLonger(t *testing.T) {
	cases := []struct {
		name        string
		sourceRich  string
		sourcePlain string
		mtPlain     string
		wantInside  string // exact text that must appear between open & close tag
	}{
		{
			name:        "bold wrap, mock [FR] prefix",
			sourceRich:  `This text is in <g id="1" ctype="bold">bold</g>`,
			sourcePlain: "This text is in bold",
			mtPlain:     "[FR] This text is in bold",
			wantInside:  "bold",
		},
		{
			name:        "italic wrap, mock [FR] prefix",
			sourceRich:  `This text is in <g id="1" ctype="italic">italics</g>`,
			sourcePlain: "This text is in italics",
			mtPlain:     "[FR] This text is in italics",
			wantInside:  "italics",
		},
		{
			name:        "anchor wrap, mock [FR] prefix",
			sourceRich:  `This is a link to <g id="1" ctype="x-html-a">machinetranslate.org</g>`,
			sourcePlain: "This is a link to machinetranslate.org",
			mtPlain:     "[FR] This is a link to machinetranslate.org",
			wantInside:  "machinetranslate.org",
		},
	}

	// Match `<g …>WANT</g>` and capture the wrap content. We assert against
	// the captured group, not on raw substring presence — the proportional
	// algorithm's buggy output (`<g …> bold</g>`) DOES contain the substring
	// "bold", so a naive Contains check would falsely pass.
	gWrap := regexp.MustCompile(`<g[^>]*>([^<]*)</g>`)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TransferTags(tc.sourceRich, tc.sourcePlain, tc.mtPlain)
			if got == "" {
				t.Fatalf("TransferTags returned empty; rich=%q plain=%q mt=%q", tc.sourceRich, tc.sourcePlain, tc.mtPlain)
			}

			m := gWrap.FindStringSubmatch(got)
			if m == nil {
				t.Fatalf("no <g …>…</g> wrap in result %q", got)
			}
			if m[1] != tc.wantInside {
				t.Errorf("wrap content mismatch:\n  result : %q\n  inside : %q\n  want   : %q\n  (opening tag drifted; outside characters got pulled into the chip)",
					got, m[1], tc.wantInside)
			}

			// Stripping the tag from the result must yield the MT plain
			// text — the algorithm should reposition the tags, NEVER drop
			// or duplicate visible characters. (Companion invariant; if
			// this fails the algorithm is also dropping/inserting text,
			// not just shifting.)
			if stripped := stripTags(got); stripped != tc.mtPlain {
				t.Errorf("stripped result = %q, want %q (visible text must match MT plain)", stripped, tc.mtPlain)
			}
		})
	}
}

// TestTransferTags_WordIndexProjection — word-index projection.
//
// Pins the common case where the wrapped word is translated (`bold`→`negrita`,
// `italics`→`cursiva`), which the verbatim anchor cannot cover.
// Word-index projection anchors each tag at the same WORD index in the MT
// output as it occupied in the source, so different word lengths don't cause
// character drift or a mid-word tag.
//
// Anti-tautology: each `want` is the full expected string. Proportional
// anchoring gives `…en n<g>egrita</g>`, `…en<g> cursiva</g>`,
// `…está <g>en curs</g>iva…` — all different from `want`, so reverting to it
// turns the test RED.
func TestTransferTags_WordIndexProjection(t *testing.T) {
	cases := []struct {
		name        string
		sourceRich  string
		sourcePlain string
		mtPlain     string
		want        string
	}{
		{
			name:        "bold→negrita (trailing word, en→es)",
			sourceRich:  `This text is in <g id="1" ctype="bold">bold</g>`,
			sourcePlain: "This text is in bold",
			mtPlain:     "Este texto está en negrita",
			want:        `Este texto está en <g id="1" ctype="bold">negrita</g>`,
		},
		{
			name:        "italics→cursiva (trailing word, en→es)",
			sourceRich:  `This text is in <g id="1" ctype="italic">italics</g>`,
			sourcePlain: "This text is in italics",
			mtPlain:     "Este texto está en cursiva",
			want:        `Este texto está en <g id="1" ctype="italic">cursiva</g>`,
		},
		{
			name:        "italics→cursiva (mid-sentence, equal word count)",
			sourceRich:  `This text is in <g id="1" ctype="italic">italics</g> and it's a repetition.`,
			sourcePlain: "This text is in italics and it's a repetition.",
			mtPlain:     "Este texto está en cursiva y es una repetición.",
			want:        `Este texto está en <g id="1" ctype="italic">cursiva</g> y es una repetición.`,
		},
		{
			name:        "anchor→link (verbatim token survives — verbatim anchor still works)",
			sourceRich:  `This is a link to <g id="1" ctype="x-html-a">machinetranslate.org</g>`,
			sourcePlain: "This is a link to machinetranslate.org",
			mtPlain:     "Este es un enlace a machinetranslate.org",
			want:        `Este es un enlace a <g id="1" ctype="x-html-a">machinetranslate.org</g>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TransferTags(tc.sourceRich, tc.sourcePlain, tc.mtPlain)
			if got != tc.want {
				t.Errorf("tag placement mismatch:\n  source: %q\n  mt:     %q\n  got:    %q\n  want:   %q",
					tc.sourceRich, tc.mtPlain, got, tc.want)
			}
			// Companion invariant: stripping tags must reproduce the MT plain
			// text exactly — never drop or duplicate visible characters.
			if stripped := stripTags(got); stripped != tc.mtPlain {
				t.Errorf("stripped result = %q, want %q (visible text must match MT plain)", stripped, tc.mtPlain)
			}
		})
	}
}

// TestTransferTags_WordCountDivergence — word-index projection, divergence path.
//
// When source/MT word counts fall outside the 0.66..1.5 ratio band the word
// index is scaled proportionally, but the anchor still snaps to a WORD
// boundary — the tag must never land mid-word. This asserts the snapping
// invariant rather than an exact placement (the scaled index is a heuristic;
// the guarantee is "between words, not inside one").
func TestTransferTags_WordCountDivergence(t *testing.T) {
	// 2 source words, the tag wraps the 2nd; MT expands to 6 words (ratio 3.0,
	// well outside the band → proportional index scaling).
	sourceRich := `Click <g id="1" ctype="bold">here</g>`
	sourcePlain := "Click here"
	mtPlain := "Haga clic en el enlace siguiente"

	got := TransferTags(sourceRich, sourcePlain, mtPlain)

	// Visible text preserved exactly.
	if stripped := stripTags(got); stripped != mtPlain {
		t.Fatalf("stripped result = %q, want %q", stripped, mtPlain)
	}
	// The wrap must hold whole words: non-empty, no edge whitespace inside,
	// and whitespace or a string edge just outside both tags.
	gWrap := regexp.MustCompile(`<g[^>]*>([^<]*)</g>`)
	m := gWrap.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no <g …>…</g> wrap in result %q", got)
	}
	inside := m[1]
	if inside == "" {
		t.Fatalf("wrap is empty — the tagged word was lost: %q", got)
	}
	ir := []rune(inside)
	if unicode.IsSpace(ir[0]) || unicode.IsSpace(ir[len(ir)-1]) {
		t.Errorf("wrap content has edge whitespace (tag not snapped to word): inside=%q in %q", inside, got)
	}
	openIdx := strings.Index(got, m[0])
	if before := []rune(stripTags(got[:openIdx])); len(before) > 0 && !unicode.IsSpace(before[len(before)-1]) {
		t.Errorf("open tag landed mid-word: char before wrap is %q in %q", string(before[len(before)-1]), got)
	}
	if after := []rune(stripTags(got[openIdx+len(m[0]):])); len(after) > 0 && !unicode.IsSpace(after[0]) {
		t.Errorf("close tag landed mid-word: char after wrap is %q in %q", string(after[0]), got)
	}
}

// TestTransferTags_CJKFallbackNoPanic — word-index projection, CJK guard.
//
// Whitespace-free target scripts (Chinese/Japanese/Thai) produce zero word
// spans, so word-index projection reports !ok and the character-proportional
// fallback runs. This must not panic and must preserve all visible characters
// (placement may drift — that's the documented limitation until a native-tag
// engine handles CJK).
func TestTransferTags_CJKFallbackNoPanic(t *testing.T) {
	sourceRich := `This is <g id="1" ctype="bold">bold</g> text`
	sourcePlain := "This is bold text"
	mtPlain := "这是粗体文字" // no spaces → zero word spans

	got := runWithTimeout(t, 2*time.Second, sourceRich, sourcePlain, mtPlain)
	if got == "" {
		t.Fatal("expected non-empty result")
	}
	if stripped := stripTags(got); stripped != mtPlain {
		t.Errorf("stripped result = %q, want %q (visible text must survive)", stripped, mtPlain)
	}
	// Tag must still be present (we don't drop it just because we can't align).
	if !strings.Contains(got, `<g id="1" ctype="bold">`) || !strings.Contains(got, "</g>") {
		t.Errorf("expected <g> wrap preserved, got %q", got)
	}
}
