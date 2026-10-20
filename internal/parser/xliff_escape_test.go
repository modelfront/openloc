package parser

import (
	"bytes"
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Inline-tag content and attributes carry XML entities (`&lt;b&gt;`, `&amp;`).
// Both export paths must write them back as well-formed XML with the same
// decoded content the source file had.
const escapedInlineXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="t.txt">
    <body>
      <trans-unit id="1">
        <source>Tom &amp; Jerry <ph id="1">&lt;br/&gt;</ph> <bpt id="2">&lt;b&gt;</bpt>bold<ept id="2">&lt;/b&gt;</ept> <ph id="3" equiv-text="a&amp;b &quot;q&quot; &lt;x&gt;">x</ph></source>
        <target>Tom &amp; Jerry <ph id="1">&lt;br/&gt;</ph> <bpt id="2">&lt;b&gt;</bpt>fett<ept id="2">&lt;/b&gt;</ept> <ph id="3" equiv-text="a&amp;b &quot;q&quot; &lt;x&gt;">x</ph></target>
      </trans-unit>
    </body>
  </file>
</xliff>`

// xliffContent decodes doc strictly and returns, per <source>/<target>, its
// text runs and inline elements (name, attributes, decoded inner text).
func xliffContent(t *testing.T, doc []byte, elem string) [][]string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = true
	var out [][]string
	var cur []string
	in := false
	var inner *strings.Builder
	var innerHead string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("exported XLIFF is not well-formed: %v\n%s", err, doc)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			if tk.Name.Local == elem {
				in, cur = true, nil
				continue
			}
			if in {
				head := tk.Name.Local
				for _, a := range tk.Attr {
					head += " " + a.Name.Local + "=" + a.Value
				}
				innerHead, inner = head, &strings.Builder{}
			}
		case xml.EndElement:
			if tk.Name.Local == elem && in {
				in = false
				out = append(out, cur)
				continue
			}
			if in && inner != nil {
				cur = append(cur, "<"+innerHead+">"+inner.String())
				inner = nil
			}
		case xml.CharData:
			if !in {
				continue
			}
			if inner != nil {
				inner.Write(tk)
			} else {
				cur = append(cur, "text:"+string(tk))
			}
		}
	}
	return out
}

func TestXLIFF_EscapedInlineContent_ReconstructRoundTrip(t *testing.T) {
	orig := []byte(escapedInlineXLIFF)
	segs, err := XLIFF{}.Parse(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 1 || segs[0].TargetRichText == "" {
		t.Fatalf("want 1 segment with rich target, got %+v", segs)
	}
	out, err := XLIFF{}.Reconstruct(orig, map[string]string{segs[0].Key: segs[0].TargetRichText})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	want := xliffContent(t, orig, "target")
	if got := xliffContent(t, out, "target"); !reflect.DeepEqual(got, want) {
		t.Errorf("target content changed on export\n got %q\nwant %q", got, want)
	}
}

func TestXLIFF_EscapedInlineContent_BilingualRoundTrip(t *testing.T) {
	orig := []byte(escapedInlineXLIFF)
	segs, err := XLIFF{}.Parse(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := segs[0]
	out, err := SerializeBilingualXLIFF("t.txt", "en", "de", []BilingualSegment{{
		Key: s.Key, SourceText: s.Text, SourceRichText: s.RichText,
		TargetText: s.Target, TargetRichText: s.TargetRichText, Status: "approved",
	}})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	for _, elem := range []string{"source", "target"} {
		want := xliffContent(t, orig, elem)
		if got := xliffContent(t, out, elem); !reflect.DeepEqual(got, want) {
			t.Errorf("%s content changed on export\n got %q\nwant %q", elem, got, want)
		}
	}
}

// A plain source shows the translator characters, and copying it to the
// target writes the same text node back.
func TestXLIFF_PlainEntitiesDecodedAndRoundTrip(t *testing.T) {
	orig := []byte(`<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2"><file source-language="en" target-language="de" datatype="plaintext" original="t.txt"><body>
<trans-unit id="1"><source>Tom &amp; Jerry &lt;5 &quot;q&quot; &apos;a&apos; &#169;</source></trans-unit>
</body></file></xliff>`)
	segs, err := XLIFF{}.Parse(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := `Tom & Jerry <5 "q" 'a' ©`; len(segs) != 1 || segs[0].Text != want || segs[0].RichText != "" {
		t.Fatalf("segments = %+v, want one plain segment with Text %q", segs, want)
	}
	out, err := XLIFF{}.Reconstruct(orig, map[string]string{segs[0].Key: segs[0].Text})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if got, want := xliffContent(t, out, "target"), xliffContent(t, orig, "source"); !reflect.DeepEqual(got, want) {
		t.Errorf("target = %q, want the source text %q", got, want)
	}
}

// Rich text saved by the editor keeps text runs unescaped, and rows stored
// before tag content was escaped hold raw markup inside <ph>/<bpt>. Export
// must still produce well-formed XML for both.
func TestXLIFF_ExportEscapesRawRichText(t *testing.T) {
	rich := `Tom & Jerry <ph id="1"><br/></ph> <bpt id="2"><b></bpt>a < b<ept id="2"></b></ept>`
	want := []string{
		"text:Tom & Jerry ", "<ph id=1><br/>", "text: ", "<bpt id=2><b>",
		"text:a < b", "<ept id=2></b>",
	}
	out, err := SerializeBilingualXLIFF("t.txt", "en", "de", []BilingualSegment{{
		Key: "1", SourceText: "x", TargetText: "x", TargetRichText: rich,
	}})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if got := xliffContent(t, out, "target"); !reflect.DeepEqual(got, [][]string{want}) {
		t.Errorf("bilingual target\n got %q\nwant %q", got, want)
	}

	orig := []byte(escapedInlineXLIFF)
	rec, err := XLIFF{}.Reconstruct(orig, map[string]string{"1": rich})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if got := xliffContent(t, rec, "target"); !reflect.DeepEqual(got, [][]string{want}) {
		t.Errorf("reconstructed target\n got %q\nwant %q", got, want)
	}
}

const mrkSegmentedXLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="de" datatype="plaintext" original="t.txt">
    <body>
      <trans-unit id="1">
        <source>Hello</source>
        <seg-source><mrk mtype="seg" mid="1">Hello</mrk></seg-source>
        <target><mrk mtype="seg" mid="1">Hallo</mrk></target>
      </trans-unit>
    </body>
  </file>
</xliff>`

// Rich text written into an <mrk mtype="seg"> target goes through the same
// escaping as a plain <target>, for raw and escaped tag content alike.
func TestXLIFF_ReconstructEscapesRichTextInMrk(t *testing.T) {
	orig := []byte(mrkSegmentedXLIFF)
	segs, err := XLIFF{}.Parse(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("want 1 segment, got %+v", segs)
	}
	want := []string{"text:Tom & Jerry ", "<ph id=1><br/>"}
	for _, rich := range []string{
		`Tom & Jerry <ph id="1"><br/></ph>`,
		`Tom & Jerry <ph id="1">&lt;br/&gt;</ph>`,
	} {
		out, err := XLIFF{}.Reconstruct(orig, map[string]string{segs[0].Key: rich})
		if err != nil {
			t.Fatalf("reconstruct: %v", err)
		}
		if got := xliffContent(t, out, "mrk"); !reflect.DeepEqual(got, [][]string{{"text:Hello"}, want}) {
			t.Errorf("rich %q: mrk content\n got %q\nwant %q", rich, got, want)
		}
	}
}
