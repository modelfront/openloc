package parser

import (
	"fmt"
	"strings"
)

// Markdown parses Markdown files, extracting translatable text from headings,
// paragraphs, and list items. Code blocks and horizontal rules are skipped.
// Inline markup (**bold**, *italic*, `code`, [links](url), ![images](url))
// is preserved inside segments.
type Markdown struct{}

func (Markdown) Parse(content []byte) ([]Segment, error) {
	lines := strings.Split(string(content), "\n")
	var segments []Segment
	seq := 0
	inFencedCode := false
	var paraLines []string

	flush := func() {
		if len(paraLines) == 0 {
			return
		}
		text := strings.Join(paraLines, "\n")
		if strings.TrimSpace(text) != "" {
			seq++
			segments = append(segments, Segment{
				Key:      fmt.Sprintf("block:%d", seq),
				Text:     text,
				Position: seq - 1,
			})
		}
		paraLines = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")

		// Toggle fenced code blocks.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			inFencedCode = !inFencedCode
			continue
		}
		if inFencedCode {
			continue
		}

		// Skip indented code blocks (4+ spaces or tab, not a list item).
		if (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) &&
			!isListItem(strings.TrimSpace(line)) {
			flush()
			continue
		}

		// Skip horizontal rules.
		trimmed := strings.TrimSpace(line)
		if isHorizontalRule(trimmed) {
			flush()
			continue
		}

		// Empty line ends current paragraph.
		if trimmed == "" {
			flush()
			continue
		}

		// Headings — each is its own segment.
		if strings.HasPrefix(trimmed, "#") {
			flush()
			seq++
			segments = append(segments, Segment{
				Key:      fmt.Sprintf("block:%d", seq),
				Text:     line,
				Position: seq - 1,
			})
			continue
		}

		// List items — each is its own segment.
		if isListItem(trimmed) {
			flush()
			seq++
			segments = append(segments, Segment{
				Key:      fmt.Sprintf("block:%d", seq),
				Text:     line,
				Position: seq - 1,
			})
			continue
		}

		// Regular text — accumulate as paragraph.
		paraLines = append(paraLines, line)
	}
	flush()

	return segments, nil
}

func (Markdown) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	lines := strings.Split(string(original), "\n")
	var result []string
	seq := 0
	inFencedCode := false
	var paraStart int
	var paraLines []string

	flushPara := func() {
		if len(paraLines) == 0 {
			return
		}
		text := strings.Join(paraLines, "\n")
		if strings.TrimSpace(text) != "" {
			seq++
			key := fmt.Sprintf("block:%d", seq)
			if translated, ok := translations[key]; ok {
				// Replace the paragraph lines with translated text.
				translatedLines := strings.Split(translated, "\n")
				// Remove original paragraph lines from result and replace.
				result = result[:paraStart]
				result = append(result, translatedLines...)
			}
		}
		paraLines = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")

		// Toggle fenced code blocks.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flushPara()
			result = append(result, line)
			inFencedCode = !inFencedCode
			continue
		}
		if inFencedCode {
			result = append(result, line)
			continue
		}

		// Skip indented code blocks.
		if (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) &&
			!isListItem(strings.TrimSpace(line)) {
			flushPara()
			result = append(result, line)
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Horizontal rules.
		if isHorizontalRule(trimmed) {
			flushPara()
			result = append(result, line)
			continue
		}

		// Empty line.
		if trimmed == "" {
			flushPara()
			result = append(result, line)
			continue
		}

		// Headings.
		if strings.HasPrefix(trimmed, "#") {
			flushPara()
			seq++
			key := fmt.Sprintf("block:%d", seq)
			if translated, ok := translations[key]; ok {
				result = append(result, translated)
			} else {
				result = append(result, line)
			}
			continue
		}

		// List items.
		if isListItem(trimmed) {
			flushPara()
			seq++
			key := fmt.Sprintf("block:%d", seq)
			if translated, ok := translations[key]; ok {
				result = append(result, translated)
			} else {
				result = append(result, line)
			}
			continue
		}

		// Paragraph text.
		if len(paraLines) == 0 {
			paraStart = len(result)
		}
		paraLines = append(paraLines, line)
		result = append(result, line)
	}
	flushPara()

	return []byte(strings.Join(result, "\n")), nil
}

// isListItem returns true if the line starts with a list marker.
func isListItem(trimmed string) bool {
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	// Ordered list: digit(s) followed by . and space.
	for i, ch := range trimmed {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '.' && i > 0 && i+1 < len(trimmed) && trimmed[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

// isHorizontalRule detects --- or *** or ___ (3+ chars, optionally spaced).
func isHorizontalRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	cleaned := strings.ReplaceAll(trimmed, " ", "")
	if len(cleaned) < 3 {
		return false
	}
	ch := cleaned[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for i := 1; i < len(cleaned); i++ {
		if cleaned[i] != ch {
			return false
		}
	}
	return true
}
