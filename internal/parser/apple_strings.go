package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// AppleStrings parses Apple .strings localization files.
// Format: "key" = "value";
type AppleStrings struct{}

func (AppleStrings) Parse(content []byte) ([]Segment, error) {
	lines := strings.Split(string(content), "\n")
	var segments []Segment
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		key, val, ok := parseStringsLine(line)
		if !ok {
			continue
		}
		segments = append(segments, Segment{
			Key:      key,
			Text:     val,
			Position: len(segments),
		})
	}
	return segments, nil
}

func (AppleStrings) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	lines := strings.Split(string(original), "\n")
	for i, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		key, _, ok := parseStringsLine(line)
		if !ok {
			continue
		}
		if translated, found := translations[key]; found {
			lines[i] = quoteStringsValue(key) + " = " + quoteStringsValue(translated) + ";"
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// quoteStringsValue returns s as a double-quoted .strings literal. Apple's
// parser knows only `\U` plus hex for Unicode escapes and reads Go's %q
// lowercase `\u` escape as plain text, so every rune other than the
// escaped five is written as itself; the file is UTF-8. Newline and CR are
// escaped because Parse reads the file one line at a time.
func quoteStringsValue(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// parseStringsLine tries to parse a "key" = "value"; line.
// Returns key, value, ok.
func parseStringsLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "//") {
		return "", "", false
	}

	// Find the key in quotes.
	key, rest, ok := extractQuoted(trimmed)
	if !ok {
		return "", "", false
	}

	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "=") {
		return "", "", false
	}
	rest = strings.TrimSpace(rest[1:])

	val, rest, ok := extractQuoted(rest)
	if !ok {
		return "", "", false
	}

	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, ";") {
		return "", "", false
	}

	return key, val, true
}

// extractQuoted extracts a double-quoted string from the start of s,
// handling escape sequences. Returns the unescaped content, the remainder, and success.
func extractQuoted(s string) (string, string, bool) {
	if len(s) == 0 || s[0] != '"' {
		return "", s, false
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"':
				b.WriteByte('"')
				i += 2
			case 'n':
				b.WriteByte('\n')
				i += 2
			case 't':
				b.WriteByte('\t')
				i += 2
			case 'r':
				b.WriteByte('\r')
				i += 2
			case '\\':
				b.WriteByte('\\')
				i += 2
			case 'U', 'u':
				// Unicode escape: \UXXXX or \uXXXX (4 hex digits)
				if i+5 < len(s) {
					hex := s[i+2 : i+6]
					r, err := parseHex4(hex)
					if err == nil {
						var buf [4]byte
						n := utf8.EncodeRune(buf[:], r)
						b.Write(buf[:n])
						i += 6
						continue
					}
				}
				b.WriteByte(s[i])
				i++
			default:
				// Any other escaped character stands for itself (Apple reads `\'` as `'`).
				b.WriteByte(s[i+1])
				i += 2
			}
		} else if s[i] == '"' {
			return b.String(), s[i+1:], true
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return "", s, false // unterminated quote
}

// parseHex4 parses a 4-digit hex string into a rune.
func parseHex4(s string) (rune, error) {
	if len(s) != 4 {
		return 0, fmt.Errorf("expected 4 hex digits")
	}
	var r rune
	for _, c := range s {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid hex digit: %c", c)
		}
	}
	return r, nil
}
