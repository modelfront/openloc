package terminology

import (
	"sort"
	"testing"
)

func TestTokenizeSourceText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		maxN   int
		expect []string
	}{
		{
			name:   "single word",
			text:   "password",
			maxN:   3,
			expect: []string{"password"},
		},
		{
			name:   "strips punctuation",
			text:   "Reset your password.",
			maxN:   2,
			expect: []string{"reset", "your", "password", "reset your", "your password", "password.", "your password."},
		},
		{
			name:   "preserves hyphens",
			text:   "Enable two-factor authentication.",
			maxN:   2,
			expect: []string{"enable", "two-factor", "authentication", "enable two-factor", "two-factor authentication", "authentication.", "two-factor authentication."},
		},
		{
			name:   "handles bullet points",
			text:   "• Click the 'Forgotten your password?' link",
			maxN:   3,
			expect: []string{
				"click", "the", "forgotten", "your", "password", "link",
				"click the", "the forgotten", "forgotten your", "your password", "password link",
				"click the forgotten", "the forgotten your", "forgotten your password", "your password link",
			},
		},
		{
			name:   "deduplicates",
			text:   "the the the",
			maxN:   2,
			expect: []string{"the", "the the"},
		},
		{
			name:   "preserves accented chars",
			text:   "Über die Straße",
			maxN:   2,
			expect: []string{"über", "die", "straße", "über die", "die straße"},
		},
		{
			name:   "maxN=1 only unigrams",
			text:   "email address reset",
			maxN:   1,
			expect: []string{"email", "address", "reset"},
		},
		// 6.5: CJK full-width punctuation should be stripped
		{
			name:   "strips CJK full-width punctuation",
			text:   "数据库，管理。系统",
			maxN:   2,
			expect: []string{"数据库", "管理", "系统", "数据库 管理", "管理 系统"},
		},
		// 6.5: CJK full-width colon and parentheses
		{
			name:   "strips CJK brackets and colons",
			text:   "设置（高级）：完成",
			maxN:   1,
			expect: []string{"设置", "高级", "完成"},
		},
		// A term with punctuation is a token as written, at any place in the
		// sentence; CJK punctuation still splits.
		{
			name:   "keeps punctuation that terms hold",
			text:   "Our R&D team (Acme Inc.) uses Node.js.",
			maxN:   1,
			expect: []string{"our", "r", "d", "team", "acme", "inc", "uses", "node", "js", "r&d", "inc.", "node.js.", "node.js"},
		},
		// A possessive/contraction apostrophe glues the word together — the
		// tokenizer must NOT split it into a bare "s"/"t" fragment (which would
		// spuriously match single-letter terms). Keep in sync with the
		// word-boundary rule in internal/qa/checker.go.
		{
			name:   "keeps an intra-word apostrophe (possessive)",
			text:   "a company's quality",
			maxN:   1,
			expect: []string{"a", "company's", "quality"},
		},
		{
			name:   "keeps an intra-word apostrophe (contraction)",
			text:   "I don't know",
			maxN:   1,
			expect: []string{"i", "don't", "know"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeSourceText(tc.text, tc.maxN)
			sort.Strings(got)
			sort.Strings(tc.expect)

			if len(got) != len(tc.expect) {
				t.Fatalf("expected %d tokens, got %d\nexpected: %v\ngot:      %v", len(tc.expect), len(got), tc.expect, got)
			}
			for i := range got {
				if got[i] != tc.expect[i] {
					t.Errorf("token[%d]: expected %q, got %q", i, tc.expect[i], got[i])
				}
			}
		})
	}
}
