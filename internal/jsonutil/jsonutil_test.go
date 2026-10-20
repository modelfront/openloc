package jsonutil

import "testing"

// Text meant for a reader — a model, a person — keeps <, > and & as they are;
// the escaping encoding/json adds by default exists only for embedding JSON in
// HTML.
func TestMarshalNoEscape_KeepsMarkupCharacters(t *testing.T) {
	got, err := MarshalNoEscape(map[string]string{"source": `See <1>details</1> & "more"`})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"source":"See <1>details</1> & \"more\""}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
