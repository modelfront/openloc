package chat

import (
	"regexp"
	"strings"
)

// pronounce rewrites what text-to-speech reads badly: product abbreviations,
// keyboard shortcuts and symbols. It runs on every spoken line, saved or
// chat-made, so a card written for the eye still sounds natural.
func pronounce(text string) string {
	for _, r := range pronunciations {
		text = r.re.ReplaceAllString(text, r.say)
	}
	return strings.Join(strings.Fields(text), " ")
}

type pronunciation struct {
	re  *regexp.Regexp
	say string
}

// Whole words only, case-sensitive, so "TM" is changed but "atm" and "Tm" are
// not. Longer forms come first ("TMX" before "TM").
var pronunciations = func() []pronunciation {
	words := []struct{ from, to string }{
		{`TMX`, "T M X"},
		{`TBX`, "T B X"},
		{`XLIFF`, "ex-liff"},
		{`DOCX`, "docx"},
		{`TMs`, "translation memories"},
		{`TM`, "translation memory"},
		{`MT`, "machine translation"},
		{`APE`, "automatic post-editing"},
		{`QE`, "quality estimation"},
		{`QA`, "Q A"},
		{`CAT`, "cat"},
		{`AI`, "A I"},
		{`PM`, "project manager"},
	}
	out := make([]pronunciation, 0, len(words)+8)
	for _, w := range words {
		out = append(out, pronunciation{regexp.MustCompile(`\b` + w.from + `\b`), w.to})
	}
	return append(out,
		pronunciation{regexp.MustCompile(`(?i)\b(ctrl|cmd|⌘)\s*\+\s*`), "Control "},
		pronunciation{regexp.MustCompile(`\s*→\s*`), ", then "},
		pronunciation{regexp.MustCompile(`\s*;\s*`), ". "},
		pronunciation{regexp.MustCompile(`\s+/\s+`), " or "},
		pronunciation{regexp.MustCompile(`[()]`), ", "},
		pronunciation{regexp.MustCompile(`\s+,`), ","},
		pronunciation{regexp.MustCompile(`,\s*([.,])`), "$1"},
		pronunciation{regexp.MustCompile(`\s*&\s*`), " and "},
	)
}()
