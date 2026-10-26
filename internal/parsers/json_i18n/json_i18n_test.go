package json_i18n

import (
	"bytes"
	"encoding/json"
	"openloc/internal/parsers/common"
	"slices"
	"strings"
	"testing"
)

func TestJSONi18n_ParseFlat(t *testing.T) {
	input := `{"hello": "Hello", "goodbye": "Goodbye", "thanks": "Thank you"}`

	p := JSONi18n{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segments))
	}

	// Check that all keys are present
	keys := map[string]string{}
	for _, s := range segments {
		keys[s.Key] = s.Text
	}

	expected := map[string]string{
		"hello":   "Hello",
		"goodbye": "Goodbye",
		"thanks":  "Thank you",
	}
	for k, v := range expected {
		if keys[k] != v {
			t.Errorf("key %q: expected %q, got %q", k, v, keys[k])
		}
	}
}

func TestJSONi18n_ParseNested(t *testing.T) {
	input := `{
		"common": {
			"save": "Save",
			"cancel": "Cancel"
		},
		"auth": {
			"login": "Log in",
			"errors": {
				"invalid": "Invalid credentials"
			}
		}
	}`

	p := JSONi18n{}
	segments, err := p.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(segments) != 4 {
		t.Fatalf("expected 4 segments, got %d", len(segments))
	}

	keys := map[string]string{}
	for _, s := range segments {
		keys[s.Key] = s.Text
	}

	expected := map[string]string{
		"common.save":         "Save",
		"common.cancel":       "Cancel",
		"auth.login":          "Log in",
		"auth.errors.invalid": "Invalid credentials",
	}
	for k, v := range expected {
		if keys[k] != v {
			t.Errorf("key %q: expected %q, got %q", k, v, keys[k])
		}
	}
}

func TestJSONi18n_ParseInvalid(t *testing.T) {
	p := JSONi18n{}
	_, err := p.Parse([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// orderCases hold key orders that are neither alphabetical nor stable under
// map iteration. Each test repeats every case, so a result in random order
// cannot pass by luck.
var orderCases = []struct {
	name  string
	input string
	keys  []string // translatable keys, in file order
}{
	{
		name:  "flat and nested",
		input: `{"zebra":"z","apple":"a","nested":{"b":"1","a":"2"},"mango":"m"}`,
		keys:  []string{"zebra", "apple", "nested.b", "nested.a", "mango"},
	},
	{
		name: "reverse alphabetical, pretty printed",
		input: `{
			"z": "1", "y": "2", "x": "3", "w": "4", "v": "5", "u": "6",
			"t": "7", "s": "8", "r": "9", "q": "10", "p": "11", "o": "12"
		}`,
		keys: []string{"z", "y", "x", "w", "v", "u", "t", "s", "r", "q", "p", "o"},
	},
	{
		name:  "deep nesting between other values",
		input: `{"title":"T","menu":{"zoom":{"out":"-","in":"+"},"about":"?"},"count":3,"body":"B"}`,
		keys:  []string{"title", "menu.zoom.out", "menu.zoom.in", "menu.about", "body"},
	},
}

const orderRuns = 20

func TestJSONi18n_ParseKeepsFileOrderCases(t *testing.T) {
	for _, tc := range orderCases {
		t.Run(tc.name, func(t *testing.T) {
			for run := range orderRuns {
				segments, err := JSONi18n{}.Parse([]byte(tc.input))
				if err != nil {
					t.Fatalf("Parse failed: %v", err)
				}
				got := make([]string, len(segments))
				for i, s := range segments {
					got[i] = s.Key
					if s.Position != i {
						t.Fatalf("run %d: %q has position %d, want %d", run, s.Key, s.Position, i)
					}
				}
				if !slices.Equal(got, tc.keys) {
					t.Fatalf("run %d: keys %v, want file order %v", run, got, tc.keys)
				}
			}
		})
	}
}

func TestJSONi18n_ReconstructKeepsFileOrder(t *testing.T) {
	for _, tc := range orderCases {
		t.Run(tc.name, func(t *testing.T) {
			first, last := tc.keys[0], tc.keys[len(tc.keys)-1]
			translations := map[string]string{first: "first translated", last: "last translated"}
			want := jsonKeyPaths(t, []byte(tc.input))

			for run := range orderRuns {
				out, err := JSONi18n{}.Reconstruct([]byte(tc.input), translations)
				if err != nil {
					t.Fatalf("Reconstruct failed: %v", err)
				}
				if got := jsonKeyPaths(t, out); !slices.Equal(got, want) {
					t.Fatalf("run %d: output keys %v, want original order %v\n%s", run, got, want, out)
				}
				segments, err := JSONi18n{}.Parse(out)
				if err != nil {
					t.Fatalf("output does not parse: %v", err)
				}
				texts := map[string]string{}
				for _, s := range segments {
					texts[s.Key] = s.Text
				}
				if texts[first] != "first translated" || texts[last] != "last translated" {
					t.Fatalf("translations not written: %q=%q, %q=%q", first, texts[first], last, texts[last])
				}
			}
		})
	}
}

// Values that are not translated keep their exact spelling: number literals
// are not rounded through float64, and objects inside arrays keep their order.
func TestJSONi18n_ReconstructKeepsOtherValues(t *testing.T) {
	original := `{"title":"Hello","count":1.50,"big":12345678901234567890,` +
		`"tags":["b",{"z":1,"a":2}],"on":true,"off":false,"none":null,"empty":{},` +
		`"body":"<b>Hi</b> & bye"}`
	translations := map[string]string{"title": "Hola & adiós"}

	out, err := JSONi18n{}.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	// Everything but the translated value comes back byte for byte, layout too.
	want := strings.Replace(original, `"title":"Hello"`, `"title":"Hola & adiós"`, 1)
	if string(out) != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestJSONi18n_ParseRejectsTrailingData(t *testing.T) {
	if _, err := (JSONi18n{}).Parse([]byte(`{"a":"A"} {"b":"B"}`)); err == nil {
		t.Fatal("expected an error for a second top-level value")
	}
}

// jsonKeyPaths lists every object key in the document as a dotted path, in
// the order the bytes hold them.
func jsonKeyPaths(t *testing.T, data []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	var paths []string
	next := func() json.Token {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("not valid JSON: %v\n%s", err, data)
		}
		return tok
	}
	var walk func(prefix string)
	walk = func(prefix string) {
		switch next() {
		case json.Delim('{'):
			for dec.More() {
				path := next().(string)
				if prefix != "" {
					path = prefix + "." + path
				}
				paths = append(paths, path)
				walk(path)
			}
			next()
		case json.Delim('['):
			for dec.More() {
				walk(prefix + "[]")
			}
			next()
		}
	}
	walk("")
	return paths
}

// Segments follow the file, so a translator sees "Open, Save, Close" in the
// order the developer wrote them, and every upload of a file gives one order.
func TestJSONi18n_ParseKeepsFileOrder(t *testing.T) {
	input := `{
  "zeta": "Z",
  "menu": {"open": "Open", "save": "Save", "close": "Close", "quit": "Quit", "help": "Help"},
  "alpha": "A",
  "list": ["skipped", {"inner": "skipped too"}],
  "mid": {"deep": {"one": "1", "two": "2"}, "after": "After"},
  "beta": "B"
}`
	want := []string{"zeta", "menu.open", "menu.save", "menu.close", "menu.quit", "menu.help", "alpha", "mid.deep.one", "mid.deep.two", "mid.after", "beta"}

	for run := 0; run < 5; run++ {
		segments, err := JSONi18n{}.Parse([]byte(input))
		if err != nil {
			t.Fatalf("Parse failed: %v", err)
		}
		got := make([]string, len(segments))
		for i, s := range segments {
			got[i] = s.Key
			if s.Position != i {
				t.Errorf("run %d: %s has position %d, want %d", run, s.Key, s.Position, i)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("run %d: keys = %v, want %v", run, got, want)
		}
	}
}

// An empty or whitespace-only value has nothing to translate.
func TestJSONi18n_ParseSkipsEmptyStrings(t *testing.T) {
	segments, err := JSONi18n{}.Parse([]byte(`{"a": "", "b": "   ", "c": "C", "n": {"d": ""}}`))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(segments) != 1 || segments[0].Key != "c" {
		t.Fatalf("segments = %+v, want only c", segments)
	}
}

// Export changes only the translated values: key order, indentation, escapes,
// HTML, numbers and lists stay exactly as the developer wrote them.
func TestJSONi18n_ReconstructKeepsEverythingElse(t *testing.T) {
	original := "{\n" +
		"    \"zeta\": \"Z\",\n" +
		"    \"intro\": \"Hi <b>there</b> & bye\",\n" +
		"    \"id\": 12345678901234567890,\n" +
		"    \"ratio\": 1.0,\n" +
		"    \"flags\": [true, null, \"x\"],\n" +
		"    \"menu\": { \"open\": \"Open\", \"save\": \"Caf\\u00e9\" },\n" +
		"    \"alpha\": \"A\"\n" +
		"}\n"
	translations := map[string]string{
		"intro":     "Hola <b>ahí</b> & adiós",
		"menu.open": "Abrir \"ya\"",
	}
	want := "{\n" +
		"    \"zeta\": \"Z\",\n" +
		"    \"intro\": \"Hola <b>ahí</b> & adiós\",\n" +
		"    \"id\": 12345678901234567890,\n" +
		"    \"ratio\": 1.0,\n" +
		"    \"flags\": [true, null, \"x\"],\n" +
		"    \"menu\": { \"open\": \"Abrir \\\"ya\\\"\", \"save\": \"Caf\\u00e9\" },\n" +
		"    \"alpha\": \"A\"\n" +
		"}\n"

	got, err := JSONi18n{}.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}
	if string(got) != want {
		t.Errorf("export changed more than the translated values.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestJSONi18n_Reconstruct(t *testing.T) {
	original := `{"hello": "Hello", "goodbye": "Goodbye"}`
	translations := map[string]string{
		"hello":   "Hola",
		"goodbye": "Adiós",
	}

	p := JSONi18n{}
	result, err := p.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if parsed["hello"] != "Hola" {
		t.Errorf("hello: expected Hola, got %s", parsed["hello"])
	}
	if parsed["goodbye"] != "Adiós" {
		t.Errorf("goodbye: expected Adiós, got %s", parsed["goodbye"])
	}
}

func TestJSONi18n_ReconstructNested(t *testing.T) {
	original := `{"ns": {"key1": "Value 1", "key2": "Value 2"}}`
	translations := map[string]string{
		"ns.key1": "Translated 1",
	}

	p := JSONi18n{}
	result, err := p.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	ns := parsed["ns"].(map[string]any)
	if ns["key1"] != "Translated 1" {
		t.Errorf("ns.key1: expected 'Translated 1', got %v", ns["key1"])
	}
	if ns["key2"] != "Value 2" {
		t.Errorf("ns.key2: expected 'Value 2' (untranslated), got %v", ns["key2"])
	}
}

func TestJSONi18n_ReconstructPartial(t *testing.T) {
	original := `{"a": "A", "b": "B", "c": "C"}`
	translations := map[string]string{
		"b": "Translated B",
	}

	p := JSONi18n{}
	result, err := p.Reconstruct([]byte(original), translations)
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if parsed["a"] != "A" {
		t.Errorf("a: expected A, got %s", parsed["a"])
	}
	if parsed["b"] != "Translated B" {
		t.Errorf("b: expected 'Translated B', got %s", parsed["b"])
	}
	if parsed["c"] != "C" {
		t.Errorf("c: expected C, got %s", parsed["c"])
	}
}

func TestFormatFromFilename(t *testing.T) {
	tests := []struct {
		name   string
		expect string
	}{
		{"en.json", "json_i18n"},
		{"messages.po", "po"},
		{"template.pot", "pot"},
		{"doc.xliff", "xliff"},
		{"doc.xlf", "xliff"},
		{"README.md", "markdown"},
		{"page.html", "html"},
		{"page.htm", "html"},
		{"doc.pdf", "pdf"},
		{"doc.docx", "docx"},
		{"Localizable.strings", "apple_strings"},
		{"strings.xml", "android_xml"},
		{"unknown.xyz", ""},
		{"EN.JSON", "json_i18n"}, // case insensitive
	}

	for _, tt := range tests {
		got := common.FormatFromFilename(tt.name)
		if got != tt.expect {
			t.Errorf("FormatFromFilename(%q) = %q, want %q", tt.name, got, tt.expect)
		}
	}
}

// A key that appears twice is one segment, and export writes its translation
// to both copies.
func TestJSONi18n_RepeatedKeyIsOneSegment(t *testing.T) {
	input := `{"a": "X", "n": {"b": "Y"}, "a": "X"}`
	segments, err := JSONi18n{}.Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	var keys []string
	for _, s := range segments {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "a,n.b" {
		t.Fatalf("keys = %v, want [a n.b]", keys)
	}

	got, err := JSONi18n{}.Reconstruct([]byte(input), map[string]string{"a": "Z"})
	if err != nil {
		t.Fatalf("Reconstruct failed: %v", err)
	}
	if want := `{"a": "Z", "n": {"b": "Y"}, "a": "Z"}`; string(got) != want {
		t.Errorf("export = %s, want %s", got, want)
	}
}
