package segmentprompt

import (
	"encoding/json"
	"testing"
)

// The generated body is what the model reads for every segment of every job, so
// these tests assert on the exact bytes rather than on a decoded map: key order
// is part of the contract (prompt caching), and a decoded map throws it away.

func TestFieldsFor_ImpliedFields(t *testing.T) {
	tests := []struct {
		mode string
		want Fields
	}{
		// MT writes the translation, so there is none to send.
		{"mt", Fields{KeySourceText}},
		{"qe", Fields{KeySourceText, KeyTargetText}},
		{"ape", Fields{KeySourceText, KeyTargetText}},
		{"qe_ape", Fields{KeySourceText, KeyTargetText}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			assertFields(t, FieldsFor(tt.mode, nil), tt.want)
		})
	}
}

func TestFieldsFor_StoredKeys(t *testing.T) {
	// Stored order does not matter: the generated object is always in canonical
	// order, so the same prompt produces byte-identical bodies run to run.
	got := FieldsFor("qe_ape", []string{KeyTerms, KeyTMMatch})
	assertFields(t, got, Fields{KeySourceText, KeyTargetText, KeyTMMatch, KeyTerms})
}

// A key the generator does not know would otherwise reach the model as an empty
// member — segment_context is a text[] an older build's UI can write too.
func TestFieldsFor_DropsUnknownKeys(t *testing.T) {
	got := FieldsFor("qe", []string{KeyTerms, "customer", "", "TM_MATCH"})
	assertFields(t, got, Fields{KeySourceText, KeyTargetText, KeyTerms})
}

// A stored segment_context can still carry retired keys such as `note` and
// `comments`; they must reach no model.
func TestFieldsFor_DropsRetiredKeys(t *testing.T) {
	for _, mode := range []string{"mt", "qe", "ape", "qe_ape"} {
		got := FieldsFor(mode, []string{KeyTMMatch, "note", KeyTerms, "comments"})
		for _, f := range got {
			if f == "note" || f == "comments" {
				t.Errorf("%s: retired key %q still generated", mode, f)
			}
		}
	}
}

// Both optional fields describe the source, so every mode offers both.
func TestOptional(t *testing.T) {
	if len(Optional) != 2 || Optional[0] != KeyTMMatch || Optional[1] != KeyTerms {
		t.Errorf("Optional = %v, want [tm_match terms]", Optional)
	}
}

func TestHas(t *testing.T) {
	f := Fields{KeySourceText, KeyTerms}
	if !f.Has(KeyTerms) {
		t.Error("Has must find a key that is present")
	}
	// The gate on every lookup: a false here is what stops a TM query the
	// prompt never asked for.
	if f.Has(KeyTMMatch) {
		t.Error("Has must not find a key that is absent")
	}
}

func TestBuild_KeyOrderAndWireNames(t *testing.T) {
	body := Build(
		FieldsFor("qe_ape", []string{KeyTMMatch, KeyTerms}),
		Values{
			SourceText: "Save the file.",
			TargetText: "Datei speichern.",
			TMMatches:  []TMMatch{{Source: "Save the document.", Target: "Dokument speichern.", Score: 87}},
			Terms:      []Term{{Source: "file", Target: "Datei", Status: "approved"}},
		},
	)

	// The model reads "source" and "translation", not the storage keys.
	const want = `{"source":"Save the file.","translation":"Datei speichern.",` +
		`"tm_matches":[{"source":"Save the document.","target":"Dokument speichern.","score":87}],` +
		`"terms":[{"source":"file","target":"Datei","status":"approved"}]}`
	if string(body) != want {
		t.Errorf("body mismatch\n got: %s\nwant: %s", body, want)
	}
}

// An empty value is omitted rather than sent as "". `"tm_matches": []` on every
// segment without one is noise that invites the model to fill it in.
func TestBuild_DropsEmptyValues(t *testing.T) {
	body := Build(
		FieldsFor("qe", []string{KeyTMMatch, KeyTerms}),
		Values{SourceText: "Save the file.", TargetText: "   "},
	)
	if string(body) != `{"source":"Save the file."}` {
		t.Errorf("only the non-empty field should survive, got: %s", body)
	}
}

// An absent target is ambiguous to the model — "identical,
// so removed" and "no match" read the same — so half a match still sends both
// members.
func TestBuild_TMMatchSendsBothHalves(t *testing.T) {
	body := Build(
		Fields{KeySourceText, KeyTMMatch},
		Values{SourceText: "Save.", TMMatches: []TMMatch{{Source: "Save the file.", Score: 80}}},
	)
	const want = `{"source":"Save.","tm_matches":[{"source":"Save the file.","target":"","score":80}]}`
	if string(body) != want {
		t.Errorf("body mismatch\n got: %s\nwant: %s", body, want)
	}
}

// A match with neither half is dropped, and a list left empty drops the key.
func TestBuild_TMMatchesDropsBlankEntries(t *testing.T) {
	body := Build(
		Fields{KeySourceText, KeyTMMatch},
		Values{SourceText: "Save.", TMMatches: []TMMatch{{Source: " ", Target: "", Score: 90}}},
	)
	if string(body) != `{"source":"Save."}` {
		t.Errorf("a blank match must not reach the model, got: %s", body)
	}
}

// Inline tags and & reach the model as written, in every member.
func TestBuild_KeepsMarkupCharacters(t *testing.T) {
	body := Build(
		Fields{KeySourceText, KeyTargetText, KeyTMMatch},
		Values{SourceText: "See <1>details</1>.", TargetText: "A & B", TMMatches: []TMMatch{{Source: "<1>Save</1>", Target: "A & B", Score: 90}}},
	)
	const want = `{"source":"See <1>details</1>.","translation":"A & B","tm_matches":[{"source":"<1>Save</1>","target":"A & B","score":90}]}`
	if string(body) != want {
		t.Errorf("body mismatch\n got: %s\nwant: %s", body, want)
	}
}

// A term's status travels because a forbidden term is the one the model most
// needs to know about.
func TestBuild_TermStatusTravels(t *testing.T) {
	body := Build(
		Fields{KeyTerms},
		Values{Terms: []Term{{Source: "bug", Target: "Wanze", Status: "forbidden"}}},
	)
	if string(body) != `{"terms":[{"source":"bug","target":"Wanze","status":"forbidden"}]}` {
		t.Errorf("status must ride along, got: %s", body)
	}
}

func TestBlank(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		// Every field dropped out — the caller falls back to its built-in body
		// rather than sending an object the model can do nothing with.
		{"all fields dropped", `{}`, true},
		{"null", `null`, true},
		{"empty", ``, true},
		{"whitespace", "  \n ", true},
		{"has content", `{"source":"Save."}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Blank(json.RawMessage(tt.body)); got != tt.want {
				t.Errorf("Blank(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// Build's output must survive a round trip — an ordered object is hand-marshalled,
// so escaping is this package's problem, not encoding/json's.
func TestBuild_EscapesValues(t *testing.T) {
	body := Build(Fields{KeySourceText}, Values{SourceText: `He said "stop"` + "\n\t"})
	var got struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("generated body must be valid JSON: %v (%s)", err, body)
	}
	if got.Source != `He said "stop"`+"\n\t" {
		t.Errorf("value did not survive the round trip: %q", got.Source)
	}
}

func assertFields(t *testing.T, got, want Fields) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fields = %v, want %v", got, want)
		}
	}
}
