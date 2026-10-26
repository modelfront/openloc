package html

import (
	"openloc/internal/parsers/common"
	"strings"
	"testing"
)

// The exported HTML is the customer's to publish, and a translation is
// whatever an editor saved. Only the formatting the editor applies and the
// source's own inline elements may come out as elements.

// A translation that carries editor formatting keeps it, and any other tag in
// it stays text.
func TestHTMLReconstructEditorFormattingEscapesOtherTags(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Hello world</p></body></html>`)
	out, err := HTML{}.Reconstruct(src, map[string]string{
		"seg:1": `<em>x</em><script>alert(1)</script><img src=x onerror=alert(1)> &amp; <strong>y</strong>`,
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	got := htmlBlocks(t, out)
	want := `[em]x[/em]<script>alert(1)</script><img src=x onerror=alert(1)> & [strong]y[/strong]`
	if len(got) != 1 || got[0] != want {
		t.Errorf("exported block = %q, want %q\n%s", got, want, out)
	}
}

// The editor's line breaks, underline, strike, code and links stay
// elements; a link keeps only a web or mail href.
func TestHTMLReconstructKeepsEveryEditorMark(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Hello world</p></body></html>`)
	out, err := HTML{}.Reconstruct(src, map[string]string{
		"seg:1": `a<br><strong>b</strong> <u>u</u> <s>s</s> <code>c</code> ` +
			`<a target="_blank" rel="noopener noreferrer nofollow" href="https://example.com/x?a=1&amp;b=2">l</a> ` +
			`<a href="javascript:alert(1)">j</a>`,
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	got := htmlBlocks(t, out)
	want := `a[br][/br][strong]b[/strong] [u]u[/u] [s]s[/s] [code]c[/code] [a]l[/a] [a]j[/a]`
	if len(got) != 1 || got[0] != want {
		t.Errorf("exported block = %q, want %q\n%s", got, want, out)
	}
	s := string(out)
	if !strings.Contains(s, `<a href="https://example.com/x?a=1&amp;b=2">l</a>`) {
		t.Errorf("editor link lost its href:\n%s", s)
	}
	if strings.Contains(s, "javascript:") {
		t.Errorf("javascript: link reached the export:\n%s", s)
	}
}

// A paragraph split in the editor is a line break in the exported block.
func TestHTMLReconstructEditorParagraphBreakIsLineBreak(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Hello world</p></body></html>`)
	out, err := HTML{}.Reconstruct(src, map[string]string{
		"seg:1": `<strong>Hi</strong></p><p>there</p><p></p><p>end`,
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	got := htmlBlocks(t, out)
	want := `[strong]Hi[/strong][br][/br]there[br][/br][br][/br]end`
	if len(got) != 1 || got[0] != want {
		t.Errorf("exported block = %q, want %q\n%s", got, want, out)
	}

	// The outer paragraph tags of a wrapped translation are dropped.
	out, err = HTML{}.Reconstruct(src, map[string]string{
		"seg:1": `<p><strong>Hi</strong></p><p>there</p>`,
	})
	if err != nil {
		t.Fatalf("reconstruct wrapped: %v", err)
	}
	got = htmlBlocks(t, out)
	want = `[strong]Hi[/strong][br][/br]there`
	if len(got) != 1 || got[0] != want {
		t.Errorf("wrapped block = %q, want %q\n%s", got, want, out)
	}
}

// A chip tag keeps only its id and ctype: attributes a translation adds to it
// never reach the export, whatever shape the chip has.
func TestHTMLReconstructChipTagsLoseForgedAttributes(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Hello world</p></body></html>`)
	for name, translated := range map[string]string{
		"plain":  `<g id="1" onmouseover="alert(1)">x</g> <x id="2" onfocus="alert(1)" autofocus/> <g id="3" ctype="x-html-strong" onclick="alert(1)">y</g> <g id="4" style="x">open`,
		"editor": `<strong>b</strong> <g id="1" onmouseover="alert(1)">x</g> <x id="2" onfocus="alert(1)"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := HTML{}.Reconstruct(src, map[string]string{"seg:1": translated})
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			s := string(out)
			for _, bad := range []string{"onmouseover", "onfocus", "onclick", "autofocus", "style="} {
				if strings.Contains(s, bad) {
					t.Errorf("forged chip attribute %q reached the export:\n%s", bad, s)
				}
			}
		})
	}
}

// A chip's start tag comes from the source segment, never from the
// translation's own original_markup.
func TestHTMLReconstructChipMarkupComesFromSource(t *testing.T) {
	src := []byte(`<html><head></head><body><p>Click <a href="https://example.com/">here</a> now</p></body></html>`)
	segs, err := HTML{}.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 1 || !strings.Contains(segs[0].RichText, `original_markup=`) {
		t.Fatalf("source segment has no chip markup: %+v", segs)
	}
	for name, forged := range map[string]string{
		"javascript href": `&lt;a href=&quot;javascript:alert(1)&quot;&gt;`,
		"img onerror":     `&lt;img src=x onerror=alert(1)&gt;`,
	} {
		t.Run(name, func(t *testing.T) {
			rich := common.OriginalMarkupAttrRE.ReplaceAllString(segs[0].RichText, `original_markup="`+forged+`"`)
			rich = strings.Replace(rich, ">here<", ">ici<", 1)
			out, err := HTML{}.Reconstruct(src, map[string]string{segs[0].Key: rich})
			if err != nil {
				t.Fatalf("reconstruct: %v", err)
			}
			s := string(out)
			if strings.Contains(s, "javascript:") || strings.Contains(s, "onerror") || strings.Contains(s, "<img") {
				t.Errorf("forged chip markup reached the export:\n%s", s)
			}
			if !strings.Contains(s, `<a href="https://example.com/">ici</a>`) {
				t.Errorf("source link lost:\n%s", s)
			}
		})
	}
}
