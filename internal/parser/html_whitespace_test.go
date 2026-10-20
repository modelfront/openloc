package parser

import (
	"strings"
	"testing"
)

// parseOneHTMLSegment parses body as an HTML page and returns its only segment.
func parseOneHTMLSegment(t *testing.T, body string) Segment {
	t.Helper()
	segs, err := HTML{}.Parse([]byte("<html><body>" + body + "</body></html>"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("want 1 segment, got %d: %+v", len(segs), segs)
	}
	return segs[0]
}

// An HTML segment's text is what the browser renders: markup indentation
// collapses to one space, and the segment's outer edges are trimmed.
func TestHTMLParse_CollapsesMarkupWhitespace(t *testing.T) {
	seg := parseOneHTMLSegment(t, `<p>
                The <strong>Early Access Program</strong> includes everything you need to switch,
                for free. Hosting, AI credits, connectors, integrations and features built for you,
                and help with your migration.
              </p>`)

	const want = "The Early Access Program includes everything you need to switch, for free. " +
		"Hosting, AI credits, connectors, integrations and features built for you, and help with your migration."
	if seg.Text != want {
		t.Errorf("Text = %q, want %q", seg.Text, want)
	}
	if strings.ContainsAny(seg.RichText, "\n\t") || !strings.HasPrefix(seg.RichText, "The <g ") ||
		!strings.HasSuffix(seg.RichText, " and help with your migration.") {
		t.Errorf("RichText = %q, want the same tidied text with the chip", seg.RichText)
	}
}

// The space next to an element is kept: an empty element may be filled by a
// script (here, the visitor's email), and the space keeps it apart from the
// next word.
func TestHTMLParse_KeepsSpaceBetweenElements(t *testing.T) {
	seg := parseOneHTMLSegment(t, `<p class="dialog-desc">
            We’ll email you at <span data-waitlist-email></span>
            <span
              data-label-demo="to schedule your demo."
              >to schedule your demo.</span
            >
          </p>`)

	if seg.Text != "We’ll email you at  to schedule your demo." {
		t.Errorf("Text = %q", seg.Text)
	}
	if !strings.HasPrefix(seg.RichText, "We’ll email you at <g ") || !strings.Contains(seg.RichText, "</g> <g ") ||
		!strings.HasSuffix(seg.RichText, ">to schedule your demo.</g>") || strings.Contains(seg.RichText, "\n") {
		t.Errorf("RichText = %q, want one space before each chip and none at the edges", seg.RichText)
	}

	out, err := HTML{}.Reconstruct([]byte("<html><body><p>We’ll email you at <span data-waitlist-email></span>\n  <span>to schedule your demo.</span></p></body></html>"),
		map[string]string{"seg:1": strings.Replace(seg.RichText, "to schedule your demo.", "para programar tu demostración.", 1)})
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if !strings.Contains(string(out), "</span> <span") {
		t.Errorf("export lost the space between the elements:\n%s", out)
	}
}

// Inside a chip at the segment's edge the browser drops the space too, and a
// line break starts its line without one.
func TestHTMLParse_TrimsEdgesInsideChipsAndAfterBreaks(t *testing.T) {
	seg := parseOneHTMLSegment(t, `<p><em
                >  Today   we
                launch </em
              ></p>`)
	if seg.Text != "Today we launch" {
		t.Errorf("Text = %q, want %q", seg.Text, "Today we launch")
	}
	if !strings.HasSuffix(seg.RichText, ">Today we launch</g>") {
		t.Errorf("RichText = %q, want no space at the chip's edges", seg.RichText)
	}

	seg = parseOneHTMLSegment(t, "<p>Line one   <br>\n     Line two</p>")
	if seg.Text != "Line one\nLine two" {
		t.Errorf("Text = %q, want %q", seg.Text, "Line one\nLine two")
	}
}

// A collapsed space stays on its side of a tag, so sentence boundaries — which
// depend on whether a space sits inside a chip — are the same as for a file
// uploaded before whitespace was collapsed, and its stored sentence
// translations export onto the right sentences.
func TestHTMLParse_CollapsedSpaceKeepsSentenceBoundaries(t *testing.T) {
	segs := parseWith(t, "html", "<html><body><p><b>One. </b>Two. Three.</p></body></html>")
	var keys []string
	for _, s := range segs {
		keys = append(keys, s.Key+"="+s.Text)
	}
	if want := "seg:1#1=One. Two.|seg:1#2=Three."; strings.Join(keys, "|") != want {
		t.Errorf("sentences = %q, want %q", strings.Join(keys, "|"), want)
	}
}

// A non-breaking space is content, not markup whitespace.
func TestHTMLParse_KeepsNonBreakingSpace(t *testing.T) {
	seg := parseOneHTMLSegment(t, "<p>No lock-in.&nbsp; No limits.</p>")
	if seg.Text != "No lock-in.  No limits." {
		t.Errorf("Text = %q", seg.Text)
	}
}
