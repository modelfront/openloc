package projectevents

import "unicode/utf8"

// ShortMessage caps an error message for event details: enough to diagnose
// from the History tab, short enough to keep rows readable. It cuts on a rune
// boundary so multibyte provider messages stay valid UTF-8.
func ShortMessage(msg string) string {
	if len(msg) <= 200 {
		return msg
	}
	cut := 200
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}
