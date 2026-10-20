package wordcount

import "testing"

// Test cases are organized by the rules in the package doc.

func TestCountWords_BasicWords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"single word", "Hello", 1},
		{"two words", "Hello world", 2},
		{"multiple words", "The quick brown fox", 4},
		{"extra whitespace", "  Hello   world  ", 2},
		{"tabs and newlines", "Hello\tworld\nfoo", 3},
		{"empty string", "", 0},
		{"whitespace only", "   \t\n  ", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_HyphenatedWords(t *testing.T) {
	// Hyphenated words count as ONE word.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"simple hyphen", "well-known", 1},
		{"double hyphen", "state-of-the-art", 1},
		{"hyphen in sentence", "This is a well-known fact", 5},
		{"chemical compound", "hydroxypropyl-beta-cyclodextrin", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_Apostrophes(t *testing.T) {
	// Apostrophe words count as ONE word.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"English contraction", "it's", 1},
		{"possession", "world's", 1},
		{"French article", "l'homme", 1},
		{"in sentence", "It's a dog's life", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_NumbersExcluded(t *testing.T) {
	// Standalone numbers are "placeables", NOT counted as words.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"standalone integer", "12345", 0},
		{"decimal", "3.14", 0},
		{"comma-separated", "1,000,000", 0},
		{"complex decimal", "1,234.56", 0},
		{"negative number", "-42", 0},
		{"positive number", "+3.5", 0},
		{"percentage", "100%", 0},
		{"currency", "$100", 0},
		{"euro currency", "€50.00", 0},
		{"number in text", "chapter 5 is good", 3},
		{"multiple numbers", "from 10 to 20", 2},
		{"just numbers", "10 20 30", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_MixedAlphanumeric(t *testing.T) {
	// Mixed alphanumeric = counted as words (contain letters, not pure numbers).
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"alphanumeric code", "B2B", 1},
		{"virus name", "COVID19", 1},
		{"ordinal", "3rd", 1},
		{"model number", "iPhone15", 1},
		{"version", "v2.0.1", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_TagsStripped(t *testing.T) {
	// Inline tags (HTML/XML markup) are stripped before counting.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"bold tag", "<b>Hello</b>", 1},
		{"nested tags", "<div><span>Hello world</span></div>", 2},
		{"self-closing tag", "Hello<br/>world", 2},
		{"tag with attributes", `<a href="url">click here</a>`, 2},
		{"only tags", "<br/><hr/>", 0},
		{"XLIFF tags", `<bpt i="1">&lt;b&gt;</bpt>word<ept i="1">&lt;/b&gt;</ept>`, 3}, // entities (&lt;b&gt;) are text, not tags → 3 tokens
		{"tags between words", "Hello<b>bold</b>world", 3},                        // tags replaced with spaces → "Hello bold world" = 3
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_PunctuationOnly(t *testing.T) {
	// Punctuation-only tokens are not counted.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"ellipsis", "...", 0},
		{"em dash", "—", 0},
		{"asterisks", "***", 0},
		{"mixed punctuation", "Hello — world", 2},
		{"slash between words", "yes/no", 1}, // single token, has letters → 1 word
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_CJK(t *testing.T) {
	// CJK: each character = 1 word.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"Chinese two chars", "翻訳", 2},
		{"Japanese hiragana", "こんにちは", 5},
		{"Japanese katakana", "カタカナ", 4},
		{"Korean hangul", "번역", 2},
		{"mixed CJK and English", "Hello世界", 3},         // "Hello" = 1 + 世 = 1 + 界 = 1
		{"CJK in sentence", "The word 翻訳 means translation", 6}, // The=1 word=1 翻=1 訳=1 means=1 translation=1
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_URLsAndEmails(t *testing.T) {
	// URLs and emails = 1 word each (single whitespace-delimited token).
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"URL", "https://example.com", 1},
		{"URL with path", "https://example.com/path/to/page", 1},
		{"email", "user@example.com", 1},
		{"URL in sentence", "Visit https://example.com today", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_RealisticSegments(t *testing.T) {
	// Real-world translation segments.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			"technical with numbers",
			"The server processes 1,000 requests per second on port 8080.",
			8, // The(1) server(2) processes(3) 1,000(skip) requests(4) per(5) second(6) on(7) port(8) 8080.(skip)
		},
		{
			"legal text with hyphens",
			"The above-mentioned party shall comply with the agreed-upon terms.",
			9,
		},
		{
			"HTML content",
			`<p>Click <a href="/help">here</a> for <b>more</b> information.</p>`,
			5, // Click(1) here(2) for(3) more(4) information.(5) — tags stripped
		},
		{
			"i18n JSON value",
			"Welcome to our platform! You have 5 new messages.",
			8, // Welcome(1) to(2) our(3) platform!(4) You(5) have(6) 5(skip) new(7) messages.(8)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_ZeroWidthSpace(t *testing.T) {
	// Zero-width space (U+200B) should act as a word boundary.
	// Common in Arabic typesetting and copy-pasted web content.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"two words joined by ZWSP", "Hello\u200Bworld", 2},
		{"three words joined by ZWSP", "Zero\u200Bwidth\u200Bspace", 3},
		{"ZWSP at edges", "\u200BHello\u200B", 1},
		{"mixed ZWSP and regular spaces", "Hello\u200Bworld how are you", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountWords_SoftHyphen(t *testing.T) {
	// Soft hyphen (U+00AD) is an invisible break hint — should act as word boundary.
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"two words joined by soft hyphen", "soft\u00ADhyphen", 2},
		{"three words", "un\u00ADbreak\u00ADable", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsCJK(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'翻', true},  // CJK Unified Ideograph
		{'の', true},  // Hiragana
		{'カ', true},  // Katakana
		{'한', true},  // Hangul
		{'A', false},  // Latin
		{'1', false},  // Digit
		{'.', false},  // Punctuation
	}
	for _, tt := range tests {
		t.Run(string(tt.r), func(t *testing.T) {
			got := isCJK(tt.r)
			if got != tt.want {
				t.Errorf("isCJK(%q) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestIsPureNumeric(t *testing.T) {
	tests := []struct {
		tok  string
		want bool
	}{
		{"123", true},
		{"3.14", true},
		{"1,000", true},
		{"-42", true},
		{"+5", true},
		{"$100", true},
		{"100%", true},
		{"B2B", false},
		{"hello", false},
		{"3rd", false},
		{"", false},
		{"...", false},
	}
	for _, tt := range tests {
		t.Run(tt.tok, func(t *testing.T) {
			got := isPureNumeric(tt.tok)
			if got != tt.want {
				t.Errorf("isPureNumeric(%q) = %v, want %v", tt.tok, got, tt.want)
			}
		})
	}
}
