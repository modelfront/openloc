package parser

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// refNodePath and refNodeContext compute a node's key and context the direct
// way, rescanning siblings for every node. The parser's keys must stay
// byte-identical to them: stored segment keys are matched across uploads.
func refNodePath(n *xmlNode) string {
	var parts []string
	for cur := n; cur != nil; cur = cur.parent {
		part := cur.name
		if cur.parent != nil {
			idx, count := 0, 0
			for _, c := range cur.parent.children {
				if c.name != cur.name {
					continue
				}
				count++
				if c == cur {
					idx = count
				}
			}
			if count > 1 {
				part = fmt.Sprintf("%s[%d]", cur.name, idx)
			}
		}
		parts = append([]string{part}, parts...)
	}
	return "/" + strings.Join(parts, "/")
}

func refNodeContext(doc *xmlDoc, n *xmlNode) string {
	for cur := n.parent; cur != nil; cur = cur.parent {
		for _, c := range cur.children {
			if c.name != "title" || c == n {
				continue
			}
			text, _ := xmlUnescape(doc.src[c.innerStart:c.innerEnd])
			text = strings.TrimSpace(collapseXMLSpace(text))
			if text == "" {
				continue
			}
			if len([]rune(text)) > maxXMLContextLen {
				text = string([]rune(text)[:maxXMLContextLen]) + "…"
			}
			return text
		}
	}
	return ""
}

// mixedKeysDoc repeats names at several levels, nests titles, has empty
// titles, a title that is itself a segment next to another title, and a
// parent with many children.
const mixedKeysDoc = `<?xml version="1.0"?>
<root>
  <title>Root title</title>
  <section><title> </title><p>First</p><p>Second</p><note>Lone note</note>
    <section><title>Inner &amp; deep</title><p>Deep one</p><list><item>A</item><item>B</item><item><p>C para</p></item></list></section>
    <section><p>No own title</p><title></title></section>
  </section>
  <section><title>Two titles</title><title>Second title</title><p>Under two</p></section>
  <chapter><p>Chapter text</p></chapter>
  <wide>%s</wide>
</root>`

func wideChildren(n int) string {
	var b strings.Builder
	for i := 0; i < n-1; i++ {
		if i%2 == 0 {
			fmt.Fprintf(&b, "<p>Paragraph %d.</p>", i)
		} else {
			fmt.Fprintf(&b, "<cell>Cell %d.</cell>", i)
		}
	}
	// The parent's only title is its last child, so finding every sibling's
	// context without a cache means scanning all of them.
	fmt.Fprintf(&b, "<title>Title %d</title>", n-1)
	return b.String()
}

// Every node's key and context, and every segment's, equal the direct
// computation, on the mixed document and on the ST4 fixture.
func TestXMLNodeKeysMatchDirectComputation(t *testing.T) {
	sources := map[string][]byte{
		"mixed": []byte(fmt.Sprintf(mixedKeysDoc, wideChildren(60))),
		"st4":   readFixture(t, st4SourcePath),
	}
	for name, src := range sources {
		doc, err := parseXMLDoc(src)
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		nodes := 0
		var walk func(n *xmlNode)
		walk = func(n *xmlNode) {
			nodes++
			if got, want := xmlNodePath(n), refNodePath(n); got != want {
				t.Errorf("%s: key %q, want %q", name, got, want)
			}
			if got, want := xmlNodeContext(doc, n), refNodeContext(doc, n); got != want {
				t.Errorf("%s: context of %s = %q, want %q", name, refNodePath(n), got, want)
			}
			for _, c := range n.children {
				walk(c)
			}
		}
		walk(doc.root)
		specs := collectXMLSegments(doc)
		if nodes < 20 || len(specs) < 10 {
			t.Fatalf("%s: fixture too small: %d nodes, %d segments", name, nodes, len(specs))
		}
		for _, s := range specs {
			if want := refNodePath(s.node); s.key != want && !strings.HasPrefix(s.key, want+"#") {
				t.Errorf("%s: segment key %q, want %q", name, s.key, want)
			}
			if want := refNodeContext(doc, s.node); s.context != want {
				t.Errorf("%s: segment context %q, want %q", name, s.context, want)
			}
		}
	}
}

// Parsing a parent with four times the children takes about four times as
// long, not sixteen: keys and contexts do not rescan the siblings per segment.
func TestXMLParseScalesLinearlyWithSiblings(t *testing.T) {
	if testing.Short() {
		t.Skip("timing comparison; runs in the full suite")
	}
	parse := func(n int) time.Duration {
		src := []byte(fmt.Sprintf(mixedKeysDoc, wideChildren(n)))
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			if _, err := (XML{}).Parse(src); err != nil {
				t.Fatal(err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := parse(3000), parse(12000)
	if ratio := float64(large) / float64(small); ratio > 8 {
		t.Errorf("4x the siblings took %.1fx as long (%v vs %v), want roughly linear", ratio, large, small)
	}
}
