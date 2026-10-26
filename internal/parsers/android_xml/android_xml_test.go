package android_xml

import (
	"strings"
	"testing"
)

func TestAndroidXML_ParseBasic(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="welcome">Welcome!</string>
    <string name="greeting">Hello, %1$s!</string>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}

	want := []struct {
		key  string
		text string
	}{
		{"app_name", "My App"},
		{"welcome", "Welcome!"},
		{"greeting", "Hello, %1$s!"},
	}
	for i, w := range want {
		if segs[i].Key != w.key {
			t.Errorf("seg[%d].Key = %q, want %q", i, segs[i].Key, w.key)
		}
		if segs[i].Text != w.text {
			t.Errorf("seg[%d].Text = %q, want %q", i, segs[i].Text, w.text)
		}
		if segs[i].Position != i {
			t.Errorf("seg[%d].Position = %d, want %d", i, segs[i].Position, i)
		}
	}
}

func TestAndroidXML_ParseSkipsTranslatableFalse(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="debug_key" translatable="false">debug_value</string>
    <string name="hello">Hello</string>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments (translatable=false skipped), got %d", len(segs))
	}
	if segs[0].Key != "app_name" {
		t.Errorf("seg[0].Key = %q, want %q", segs[0].Key, "app_name")
	}
	if segs[1].Key != "hello" {
		t.Errorf("seg[1].Key = %q, want %q", segs[1].Key, "hello")
	}
}

func TestAndroidXML_ParseStringArray(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="colors">
        <item>Red</item>
        <item>Blue</item>
        <item>Green</item>
    </string-array>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}

	want := []struct {
		key  string
		text string
	}{
		{"colors[0]", "Red"},
		{"colors[1]", "Blue"},
		{"colors[2]", "Green"},
	}
	for i, w := range want {
		if segs[i].Key != w.key {
			t.Errorf("seg[%d].Key = %q, want %q", i, segs[i].Key, w.key)
		}
		if segs[i].Text != w.text {
			t.Errorf("seg[%d].Text = %q, want %q", i, segs[i].Text, w.text)
		}
	}
}

func TestAndroidXML_ParsePlurals(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <plurals name="items">
        <item quantity="one">%d item</item>
        <item quantity="other">%d items</item>
    </plurals>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}

	if segs[0].Key != "items[one]" {
		t.Errorf("seg[0].Key = %q, want %q", segs[0].Key, "items[one]")
	}
	if segs[0].Text != "%d item" {
		t.Errorf("seg[0].Text = %q, want %q", segs[0].Text, "%d item")
	}
	if segs[1].Key != "items[other]" {
		t.Errorf("seg[1].Key = %q, want %q", segs[1].Key, "items[other]")
	}
	if segs[1].Text != "%d items" {
		t.Errorf("seg[1].Text = %q, want %q", segs[1].Text, "%d items")
	}
}

func TestAndroidXML_ReconstructRoundTrip(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <!-- Welcome screen -->
    <string name="app_name">My App</string>
    <string name="welcome">Welcome!</string>
    <string name="debug" translatable="false">dbg</string>
    <string-array name="colors">
        <item>Red</item>
        <item>Blue</item>
    </string-array>
    <plurals name="items">
        <item quantity="one">%d item</item>
        <item quantity="other">%d items</item>
    </plurals>
</resources>`

	translations := map[string]string{
		"app_name":     "Mi App",
		"welcome":      "Bienvenido!",
		"colors[0]":    "Rojo",
		"colors[1]":    "Azul",
		"items[one]":   "%d elemento",
		"items[other]": "%d elementos",
	}

	p := AndroidXML{}
	result, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct error: %v", err)
	}

	out := string(result)

	// Translated strings should appear.
	if !strings.Contains(out, "Mi App") {
		t.Error("expected 'Mi App' in output")
	}
	if !strings.Contains(out, "Bienvenido!") {
		t.Error("expected 'Bienvenido!' in output")
	}
	// Non-translatable should be preserved.
	if !strings.Contains(out, "dbg") {
		t.Error("expected non-translatable 'dbg' preserved")
	}
	// Array translations.
	if !strings.Contains(out, "Rojo") {
		t.Error("expected 'Rojo' in output")
	}
	if !strings.Contains(out, "Azul") {
		t.Error("expected 'Azul' in output")
	}
	// Plural translations.
	if !strings.Contains(out, "elemento") {
		t.Error("expected 'elemento' in output")
	}
	if !strings.Contains(out, "elementos") {
		t.Error("expected 'elementos' in output")
	}
	// Comment preserved.
	if !strings.Contains(out, "Welcome screen") {
		t.Error("expected comment 'Welcome screen' preserved")
	}

	// Round-trip: parse the reconstructed output and verify translations took effect.
	segs, err := p.Parse(result)
	if err != nil {
		t.Fatalf("Parse reconstructed output error: %v", err)
	}
	segMap := make(map[string]string)
	for _, s := range segs {
		segMap[s.Key] = s.Text
	}
	if segMap["app_name"] != "Mi App" {
		t.Errorf("round-trip app_name = %q, want %q", segMap["app_name"], "Mi App")
	}
	if segMap["colors[0]"] != "Rojo" {
		t.Errorf("round-trip colors[0] = %q, want %q", segMap["colors[0]"], "Rojo")
	}
	if segMap["items[one]"] != "%d elemento" {
		t.Errorf("round-trip items[one] = %q, want %q", segMap["items[one]"], "%d elemento")
	}
}

// The translator sees characters, not entities, and export escapes them once.
func TestAndroidXML_EntitiesDecodedAndEscapedOnce(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="terms">Terms &amp; Conditions</string>
    <string name="compare">A &lt; B &gt; C \&quot;q\&quot; &#233;</string>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].Text != "Terms & Conditions" {
		t.Errorf("terms = %q, want %q", segs[0].Text, "Terms & Conditions")
	}
	if segs[1].Text != `A < B > C "q" é` {
		t.Errorf("compare = %q, want %q", segs[1].Text, `A < B > C "q" é`)
	}

	result, err := p.Reconstruct([]byte(input), map[string]string{
		"terms":   "Términos & Condiciones",
		"compare": "A < B > C",
	})
	if err != nil {
		t.Fatalf("Reconstruct error: %v", err)
	}
	out := string(result)
	for _, want := range []string{
		`<string name="terms">Términos &amp; Condiciones</string>`,
		`<string name="compare">A &lt; B &gt; C</string>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s\ngot:\n%s", want, out)
		}
	}
}

func TestAndroidXML_InlineFormatting(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="styled">This is <b>bold</b> and <i>italic</i></string>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	// Inline HTML is detected at parse — Text strips the tags,
	// RichText carries the XLIFF <g> wrappers so the CAT editor renders chips.
	if segs[0].Text != "This is bold and italic" {
		t.Errorf("expected plain text 'This is bold and italic', got %q", segs[0].Text)
	}
	// IDs are assigned by detection order — bold/italic could end up as
	// id=1 or id=2 depending on which tag the detector encounters first.
	// Assert on ctype + inner content only.
	if !strings.Contains(segs[0].RichText, `ctype="bold">bold</g>`) {
		t.Errorf("expected RichText to wrap 'bold' in <g ctype=bold>, got %q", segs[0].RichText)
	}
	if !strings.Contains(segs[0].RichText, `ctype="italic">italic</g>`) {
		t.Errorf("expected RichText to wrap 'italic' in <g ctype=italic>, got %q", segs[0].RichText)
	}
}

func TestAndroidXML_CDATA(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="html_content"><![CDATA[<h1>Hello</h1>]]></string>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	// CDATA content should be captured (encoding/xml strips the CDATA wrapper
	// but preserves the content in innerxml as escaped text or raw text).
	if !strings.Contains(segs[0].Text, "Hello") {
		t.Errorf("expected CDATA content preserved, got %q", segs[0].Text)
	}
}

func TestAndroidXML_ParseMultipleArrays(t *testing.T) {
	input := `<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string-array name="colors">
        <item>Red</item>
        <item>Blue</item>
    </string-array>
    <string-array name="sizes">
        <item>Small</item>
        <item>Medium</item>
        <item>Large</item>
    </string-array>
</resources>`

	p := AndroidXML{}
	segs, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(segs) != 5 {
		t.Fatalf("expected 5 segments, got %d", len(segs))
	}

	// Verify second array indices start at 0, not continue from first array
	want := []struct {
		key  string
		text string
	}{
		{"colors[0]", "Red"},
		{"colors[1]", "Blue"},
		{"sizes[0]", "Small"},
		{"sizes[1]", "Medium"},
		{"sizes[2]", "Large"},
	}
	for i, w := range want {
		if segs[i].Key != w.key {
			t.Errorf("seg[%d].Key = %q, want %q", i, segs[i].Key, w.key)
		}
		if segs[i].Text != w.text {
			t.Errorf("seg[%d].Text = %q, want %q", i, segs[i].Text, w.text)
		}
	}

	// Round-trip reconstruct with translations
	translations := map[string]string{
		"colors[0]": "Rojo",
		"colors[1]": "Azul",
		"sizes[0]":  "Pequeño",
		"sizes[1]":  "Mediano",
		"sizes[2]":  "Grande",
	}
	out, err := p.Reconstruct([]byte(input), translations)
	if err != nil {
		t.Fatalf("Reconstruct error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "Rojo") {
		t.Error("expected 'Rojo' in output")
	}
	if !strings.Contains(result, "Pequeño") {
		t.Error("expected 'Pequeño' in output")
	}
	if !strings.Contains(result, "Grande") {
		t.Error("expected 'Grande' in output")
	}
}
