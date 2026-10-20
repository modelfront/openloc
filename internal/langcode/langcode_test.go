package langcode

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Basic lowercase
		{"en", "en"},
		{"fr", "fr"},
		{"de", "de"},

		// Uppercase → lowercase
		{"EN", "en"},
		{"FR", "fr"},
		{"EN-US", "en-us"},
		{"PT-BR", "pt-br"},
		{"ZH-HANS", "zh-hans"},

		// Mixed case
		{"fr-FR", "fr-fr"},
		{"en-Us", "en-us"},
		{"zh-Hans", "zh-hans"},
		{"sr-Latn", "sr-latn"},
		{"sr-Cyrl", "sr-cyrl"},

		// Underscore → hyphen
		{"pt_BR", "pt-br"},
		{"en_US", "en-us"},
		{"zh_CN", "zh-cn"},

		// Whitespace trimming
		{" en ", "en"},
		{" fr-FR ", "fr-fr"},
		{"\ten\n", "en"},

		// Empty / blank
		{"", ""},
		{"  ", ""},
		{"\t", ""},

		// Legacy aliases
		{"iw", "he"},       // Hebrew
		{"jw", "jv"},       // Javanese
		{"in", "id"},       // Indonesian
		{"mo", "ro"},       // Moldavian
		{"fil", "tl"},      // Filipino
		{"nb", "nb"},       // Norwegian Bokmål — pass-through (NOT mapped to `no`)
		{"nb-NO", "nb-no"}, // Bokmål regional — must NOT be normalized to `no-no`
		{"nn", "nn"},       // Norwegian Nynorsk — also distinct from `no`
		{"IW", "he"},       // alias + uppercase
		{"iw-IL", "he-il"}, // alias with region

		// Three-letter codes
		{"ceb", "ceb"},
		{"haw", "haw"},

		// Already canonical
		{"en-us", "en-us"},
		{"pt-br", "pt-br"},
		{"zh-hans", "zh-hans"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Normalize(tt.input)
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeAll(t *testing.T) {
	input := []string{"EN", "fr-FR", "pt_BR", ""}
	want := []string{"en", "fr-fr", "pt-br", ""}

	got := NormalizeAll(input)
	if len(got) != len(want) {
		t.Fatalf("NormalizeAll length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NormalizeAll[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBaseLang(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"fr-fr", "fr"},
		{"pt-br", "pt"},
		{"en-us", "en"},
		{"zh-hans", "zh"},
		{"en", "en"},
		{"fr", "fr"},
		{"sr-latn", "sr"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := BaseLang(tt.input)
			if got != tt.want {
				t.Errorf("BaseLang(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSameBase(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"en", "en", true},
		{"en-us", "en", true},
		{"en", "en-us", true},
		{"en-us", "en-gb", true},
		{"zh-hans", "zh-hant", true},
		{"en", "fr", false},
		{"en-us", "es-us", false},
		// Un-normalized input isn't rescued — callers normalize first.
		{"en_us", "en", false},
		{"", "", true},
		{"", "en", false},
	}

	for _, tt := range tests {
		t.Run(tt.a+"|"+tt.b, func(t *testing.T) {
			if got := SameBase(tt.a, tt.b); got != tt.want {
				t.Errorf("SameBase(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// Regression test for the exact bug that prompted this package:
// Template TM assignment uses "fr-FR", project uses "fr".
// After normalization, "fr-FR" becomes "fr-fr" (not "fr").
// These are genuinely different codes and should NOT match via exact equality.
// The normalization ensures consistent formatting, not merging of different codes.
func TestNormalize_RegressionFrVariants(t *testing.T) {
	// "fr-FR" and "fr" are different codes — normalization doesn't merge them
	frFR := Normalize("fr-FR")
	fr := Normalize("fr")

	if frFR != "fr-fr" {
		t.Errorf("Normalize(fr-FR) = %q, want fr-fr", frFR)
	}
	if fr != "fr" {
		t.Errorf("Normalize(fr) = %q, want fr", fr)
	}
	if frFR == fr {
		t.Error("fr-fr and fr should NOT be equal — they are different language codes")
	}

	// But BaseLang should match for future Phase 2 fallback
	if BaseLang(frFR) != BaseLang(fr) {
		t.Error("BaseLang(fr-fr) should equal BaseLang(fr)")
	}
}

// Ensure that the same code in different formats all normalize to the same thing.
func TestNormalize_Consistency(t *testing.T) {
	variants := []string{"PT-BR", "pt-br", "pt_BR", "Pt-Br", "pt_br", " PT-BR "}
	expected := "pt-br"

	for _, v := range variants {
		got := Normalize(v)
		if got != expected {
			t.Errorf("Normalize(%q) = %q, want %q", v, got, expected)
		}
	}
}
