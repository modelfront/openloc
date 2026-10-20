package wordcount

import (
	"strings"
	"testing"
)

// TestCountWordsVsNaive documents exactly where CountWords differs from naive
// strings.Fields counting, and so what naive counting would get wrong.
func TestCountWordsVsNaive(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int // CountWords
		naive   int // len(strings.Fields(text))
		diffPct string
		reason  string
	}{
		{
			name:    "standalone number excluded",
			input:   "Process 1000 requests",
			want:    2,
			naive:   3,
			diffPct: "33%",
			reason:  "CountWords excludes standalone numbers (placeables)",
		},
		{
			name:    "currency excluded",
			input:   "Price is $99.99 per unit",
			want:    4, // Price(1) is(2) $99.99(skip) per(3) unit(4)
			naive:   5,
			diffPct: "20%",
			reason:  "CountWords excludes currency amounts",
		},
		{
			name:    "tags stripped",
			input:   "<b>Bold</b> and <i>italic</i>",
			want:    3, // Bold(1) and(2) italic(3) — tags become spaces
			naive:   3, // <b>Bold</b>(1) and(2) <i>italic</i>(3) — tags stay as part of token
			diffPct: "0%",
			reason:  "Tags stripped vs kept — same count here because tags are adjacent to words",
		},
		{
			name:    "CJK per-character",
			input:   "翻訳メモリ",
			want:    5,
			naive:   1,
			diffPct: "400%",
			reason:  "CountWords counts each CJK character as one word",
		},
		{
			name:    "punctuation-only excluded",
			input:   "Hello ... world",
			want:    2,
			naive:   3,
			diffPct: "33%",
			reason:  "CountWords excludes punctuation-only tokens",
		},
		{
			name:    "hyphenated stays one (same as naive)",
			input:   "well-known fact",
			want:    2,
			naive:   2,
			diffPct: "0%",
			reason:  "Both treat hyphenated as one token (whitespace split)",
		},
		{
			name:    "realistic technical segment",
			input:   "The <code>API</code> processes 10,000 requests at $0.01 each — totaling $100.00",
			want:    7,  // The(1) API(2) processes(3) 10,000(skip) requests(4) at(5) $0.01(skip) each(6) —(skip) totaling(7) $100.00(skip)
			naive:   11, // All whitespace tokens counted including tags, numbers, currency, em-dash
			diffPct: "36%",
			reason:  "Technical content with numbers, currency, tags, and punctuation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWords := CountWords(tt.input)
			gotNaive := len(strings.Fields(tt.input))

			if gotWords != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, gotWords, tt.want)
			}
			if gotNaive != tt.naive {
				t.Errorf("len(strings.Fields(%q)) = %d, want %d (naive)", tt.input, gotNaive, tt.naive)
			}

			t.Logf("CountWords=%d, Naive=%d, diff=%s — %s", gotWords, gotNaive, tt.diffPct, tt.reason)
		})
	}
}

// TestCountWordsVsGMXV documents where our counts differ from the GMX-V
// standard. Main difference: GMX-V counts hyphenated words as 2.
func TestCountWordsVsGMXV(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int // our count
		gmxv   int // what GMX-V would produce
		reason string
	}{
		{
			name:   "hyphenated word",
			input:  "well-known",
			want:   1,
			gmxv:   2,
			reason: "GMX-V splits on hyphens, CountWords keeps them together",
		},
		{
			name:   "multi-hyphen",
			input:  "state-of-the-art design",
			want:   2,
			gmxv:   5,
			reason: "GMX-V: state(1) of(2) the(3) art(4) design(5); CountWords: state-of-the-art(1) design(2)",
		},
		{
			name:   "no hyphens — same count",
			input:  "simple words here",
			want:   3,
			gmxv:   3,
			reason: "No hyphens → identical counts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountWords(tt.input)
			if got != tt.want {
				t.Errorf("CountWords(%q) = %d, want %d", tt.input, got, tt.want)
			}
			t.Logf("CountWords=%d, GMX-V=%d — %s", tt.want, tt.gmxv, tt.reason)
		})
	}
}
