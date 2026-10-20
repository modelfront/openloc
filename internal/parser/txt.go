package parser

import (
	"fmt"
	"strings"
)

// TXT parses plain text files, treating each non-empty line as a translatable segment.
type TXT struct {
	// PlainText says the file's strings are plain text, not HTML (see GetParserFor).
	PlainText bool
}

func (p TXT) Parse(content []byte) ([]Segment, error) {
	lines := strings.Split(string(content), "\n")
	var segments []Segment
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		plain, rich := importString(trimmed, p.PlainText)
		segments = append(segments, Segment{
			Key:      fmt.Sprintf("line:%d", i+1),
			Text:     plain,
			RichText: rich,
			Position: len(segments),
		})
	}
	return segments, nil
}

func (p TXT) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	lines := strings.Split(string(original), "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		key := fmt.Sprintf("line:%d", i+1)
		if translated, ok := translations[key]; ok {
			lines[i] = exportString(translated, trimmed, p.PlainText)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}
