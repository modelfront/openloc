package parser

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML parses HTML files. Segmentation is described in html_segment.go.
type HTML struct {
	// stored is each segment's source as stored, by key, for export: it says
	// which chip numbering the stored translation uses (see
	// HTML.chipNumbering). Nil reads every translation by the run's own. A
	// pointer, so HTML values stay comparable.
	stored *map[string]StoredSource
}

// StoredSource is a segment's source text and rich text as stored.
type StoredSource struct {
	Text, RichText string
}

// HTMLWithStoredSource is the HTML parser for exporting translations whose
// segments store the given source, by segment key. It splits sentences as
// GetParser does, so a file stored by sentence exports through it too.
func HTMLWithStoredSource(stored map[string]StoredSource) Parser {
	return sentenceSegmenter{inner: HTML{stored: &stored}, richExport: true}
}

func (HTML) Parse(content []byte) ([]Segment, error) {
	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("html parse: %w", err)
	}

	var segments []Segment
	seq := 0
	extractMeta(doc, &segments, &seq)

	for _, u := range collectHTMLUnits(doc, seq) {
		segments = append(segments, Segment{
			Key:      u.key,
			Text:     u.plain,
			RichText: u.rich,
			Position: len(segments),
			Context:  u.context,
		})
	}
	return segments, nil
}

func (p HTML) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	return p.reconstructJoined(original, translations, nil)
}

// reconstructJoined is Reconstruct with the blocks a sentence split joined
// back, as pieces by block key: a run is written by joinedHTMLMarkup, an
// attribute or the meta description takes the plain join.
func (p HTML) reconstructJoined(original []byte, translations map[string]string, joined map[string][]sentencePiece) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(original))
	if err != nil {
		return nil, fmt.Errorf("html parse: %w", err)
	}
	plain := translations
	if len(joined) > 0 {
		plain = make(map[string]string, len(translations)+len(joined))
		for k, v := range translations {
			plain[k] = v
		}
		for k, pieces := range joined {
			plain[k] = joinPieces(pieces)
		}
	}

	seq := 0
	reconstructMeta(doc, plain, &seq)

	units := collectHTMLUnits(doc, seq)
	// Attributes go first: a run's translation copies its inline elements,
	// attributes included.
	for _, u := range units {
		if translated, ok := plain[u.key]; ok && !u.isRun {
			setAttr(u.elem, u.attr, translated)
		}
	}
	for _, u := range units {
		if !u.isRun {
			continue
		}
		var markup string
		if pieces, ok := joined[u.key]; ok {
			markup = joinedHTMLMarkup(pieces)
		} else if translated, ok := translations[u.key]; ok {
			markup = htmlTranslationMarkup(translated)
		} else {
			continue
		}
		inlines, wholeBlock := p.chipNumbering(u)
		if wholeBlock {
			u.parent = u.block
			u.nodes = nil
			for c := u.block.FirstChild; c != nil; c = c.NextSibling {
				u.nodes = append(u.nodes, c)
			}
		}
		if err := replaceHTMLRun(u, markup, inlines); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return nil, fmt.Errorf("html render: %w", err)
	}
	return buf.Bytes(), nil
}

// extractMeta finds <meta name="description" content="..."> and adds a segment.
func extractMeta(doc *html.Node, segments *[]Segment, seq *int) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Meta {
			name := getAttr(n, "name")
			content := getAttr(n, "content")
			if strings.EqualFold(name, "description") && strings.TrimSpace(content) != "" {
				*seq++
				*segments = append(*segments, Segment{
					Key:      fmt.Sprintf("seg:%d", *seq),
					Text:     content,
					Position: *seq - 1,
					Context:  "meta:description",
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// reconstructMeta replaces meta description content attribute.
func reconstructMeta(doc *html.Node, translations map[string]string, seq *int) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Meta {
			name := getAttr(n, "name")
			content := getAttr(n, "content")
			if strings.EqualFold(name, "description") && strings.TrimSpace(content) != "" {
				*seq++
				key := fmt.Sprintf("seg:%d", *seq)
				if translated, ok := translations[key]; ok {
					setAttr(n, "content", translated)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// htmlStartTag renders a node's opening tag, attributes included, in the form
// it would appear in the document: `<a href="https://example.com">`.
func htmlStartTag(n *html.Node) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(n.Data)
	for _, a := range n.Attr {
		b.WriteByte(' ')
		if a.Namespace != "" {
			b.WriteString(a.Namespace)
			b.WriteByte(':')
		}
		b.WriteString(a.Key)
		b.WriteString(`="`)
		b.WriteString(xmlEscapeAttr(a.Val))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	return b.String()
}

func hasAttr(n *html.Node, key, val string) bool {
	for _, a := range n.Attr {
		if a.Key == key && a.Val == val {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}
