package chat

import (
	"strings"
	"testing"
)

// A model reply is untrusted input on a UI surface: whatever comes back becomes
// a prompt the user can send with one keypress, so anything unusable has to
// degrade to the greyed hint rather than reach the card.
func TestSanitizeSuggestion(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"plain sentence passes",
			"Create a chart of words processed by target language and save it to the Operations dashboard.",
			"Create a chart of words processed by target language and save it to the Operations dashboard.",
		},
		{
			"strips quotes and surrounding space",
			`  "Create a chart of QA issues by check type on the Quality dashboard."  `,
			"Create a chart of QA issues by check type on the Quality dashboard.",
		},
		{
			"keeps only the first line",
			"Create a chart of editing minutes per linguist on the Team dashboard.\nHere's why that's useful…",
			"Create a chart of editing minutes per linguist on the Team dashboard.",
		},
		// The dashboard's own date-range and granularity controls own the
		// window, so a suggestion naming one is wrong as soon as anyone moves a
		// dropdown. The model is told this; this is what happens when it forgets.
		{"rejects a window", "Create a chart of words processed over the last 90 days.", ""},
		{"rejects an interval", "Create a chart of words processed per week on Operations.", ""},
		{"rejects a grain adjective", "Create a weekly chart of MT volume by engine.", ""},
		{"rejects too short", "Do it", ""},
		{"rejects empty", "   ", ""},
		{"rejects an essay", "Create a chart. " + strings.Repeat("x", 400), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeSuggestion(tt.raw); got != tt.want {
				t.Errorf("sanitizeSuggestion(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// The greyed placeholder is an EXAMPLE of what to type, so it must not carry
// the window language the suggestions themselves are forbidden from using.
func TestFallbackHintNamesNoWindow(t *testing.T) {
	if windowMention.MatchString(fallbackHint) {
		t.Errorf("fallback hint names a window or interval: %q", fallbackHint)
	}
	if len(fallbackHint) < 15 {
		t.Errorf("fallback hint too short to be useful: %q", fallbackHint)
	}
}
