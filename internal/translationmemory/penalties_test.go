package translationmemory

import (
	"math"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// Deduction is the single number the whole feature turns on: it sets the
// displayed percentage AND decides auto-confirm (deduction == 0 means "nothing
// docked this match, sign it off").
func TestPenalties_Deduction(t *testing.T) {
	p := Penalties{
		Sublanguage: 0.01,
		NearExact:   0.02,
		PerTM:       map[int64]float64{7: 0.05},
	}
	sameLangs := MatchInfo{WantSrc: "en", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}

	tests := []struct {
		name string
		in   MatchInfo
		want float64
	}{
		{"nothing_applies", sameLangs, 0},
		{"source_variant", MatchInfo{WantSrc: "en-us", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}, 0.01},
		{"target_variant", MatchInfo{WantSrc: "en", GotSrc: "en", WantTgt: "fr-fr", GotTgt: "fr"}, 0.01},
		{"both_sides", MatchInfo{WantSrc: "en-us", GotSrc: "en", WantTgt: "fr-fr", GotTgt: "fr-ca"}, 0.02},
		{"per_tm_only", MatchInfo{TMID: 7, WantSrc: "en", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}, 0.05},
		{"unpenalized_tm", MatchInfo{TMID: 8, WantSrc: "en", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}, 0},
		{"near_exact_only", MatchInfo{WantSrc: "en", GotSrc: "en", WantTgt: "fr", GotTgt: "fr", NearExact: true}, 0.02},
		{
			"all_three_stack",
			MatchInfo{TMID: 7, WantSrc: "en-us", GotSrc: "en", WantTgt: "fr-fr", GotTgt: "fr-ca", NearExact: true},
			0.02 + 0.05 + 0.02,
		},
		// Different base languages aren't a sublanguage mismatch — the lookups
		// can't return those at all, so there's nothing to charge for.
		{"different_base_untouched", MatchInfo{WantSrc: "en", GotSrc: "de", WantTgt: "fr", GotTgt: "fr"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Deduction(tt.in); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Deduction(%+v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestPenalties_Apply(t *testing.T) {
	p := Penalties{Sublanguage: 0.01, NearExact: 0.01, PerTM: map[int64]float64{7: 0.05}}
	same := MatchInfo{WantSrc: "en", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}

	t.Run("clean_match_passes_through", func(t *testing.T) {
		if got := p.Apply(1.0, same); got != 1.0 {
			t.Errorf("Apply(1.0) = %v, want 1.0", got)
		}
		if got := p.Apply(0.85, same); math.Abs(got-0.85) > 1e-9 {
			t.Errorf("Apply(0.85) = %v, want 0.85", got)
		}
	})

	// A near-exact hit is an exact match docked the near-exact penalty, NOT the
	// raw Levenshtein — that's the whole point of the band. A trailing period on
	// a short segment scores ~0.9 raw and must still read 99%.
	t.Run("near_exact_scores_from_one_not_raw", func(t *testing.T) {
		nearExact := same
		nearExact.NearExact = true
		if got := p.Apply(0.90, nearExact); math.Abs(got-0.99) > 1e-9 {
			t.Errorf("Apply(0.90, near-exact) = %v, want 0.99", got)
		}
	})

	t.Run("penalties_stack_on_a_fuzzy_score", func(t *testing.T) {
		in := MatchInfo{TMID: 7, WantSrc: "en-us", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}
		if got := p.Apply(0.85, in); math.Abs(got-(0.85-0.01-0.05)) > 1e-9 {
			t.Errorf("Apply(0.85) = %v, want %v", got, 0.85-0.06)
		}
	})

	t.Run("floors_at_zero", func(t *testing.T) {
		heavy := Penalties{Sublanguage: 0.9, PerTM: map[int64]float64{7: 0.9}}
		in := MatchInfo{TMID: 7, WantSrc: "en-us", GotSrc: "en", WantTgt: "fr", GotTgt: "fr"}
		if got := heavy.Apply(0.5, in); got != 0 {
			t.Errorf("Apply = %v, want 0 (floored)", got)
		}
	})

	// Zero penalties are what "locales are equivalent" means: the variant keeps
	// its full score, and Deduction 0 is what lets the caller auto-confirm it.
	t.Run("zero_penalties_leave_a_variant_untouched", func(t *testing.T) {
		none := Penalties{}
		in := MatchInfo{TMID: 7, WantSrc: "en-us", GotSrc: "en", WantTgt: "fr-fr", GotTgt: "fr-ca"}
		if got := none.Apply(1.0, in); got != 1.0 {
			t.Errorf("Apply = %v, want 1.0", got)
		}
		if got := none.Deduction(in); got != 0 {
			t.Errorf("Deduction = %v, want 0", got)
		}
	})
}

func TestPenaltiesFromProfile(t *testing.T) {
	got := PenaltiesFromProfile(db.SettingsProfile{
		SublanguagePenaltyPct: 5,
		NearExactPenaltyPct:   2,
	})
	if math.Abs(got.Sublanguage-0.05) > 1e-9 {
		t.Errorf("Sublanguage = %v, want 0.05", got.Sublanguage)
	}
	if math.Abs(got.NearExact-0.02) > 1e-9 {
		t.Errorf("NearExact = %v, want 0.02", got.NearExact)
	}
	if got.PerTM != nil {
		t.Errorf("PerTM = %v, want nil until WithTMs is called", got.PerTM)
	}
}

func TestPenalties_WithTMs(t *testing.T) {
	base := Penalties{Sublanguage: 0.01}
	got := base.WithTMs([]db.LookupSettingsTMsRow{
		{TmID: 3, PenaltyPct: 0},
		{TmID: 4, PenaltyPct: 10},
	})

	if got.PerTM[4] != 0.10 {
		t.Errorf("PerTM[4] = %v, want 0.10", got.PerTM[4])
	}
	// A 0% assignment must not land in the map — an entry of 0 is the same as
	// absent, and keeping it out means Deduction stays allocation-light.
	if _, ok := got.PerTM[3]; ok {
		t.Errorf("PerTM should omit zero-penalty TMs, got %v", got.PerTM)
	}
	if got.Sublanguage != base.Sublanguage {
		t.Errorf("WithTMs clobbered Sublanguage: %v", got.Sublanguage)
	}
	if base.PerTM != nil {
		t.Error("WithTMs mutated the receiver's PerTM")
	}
}

// IsNearExact draws the line the band depends on: byte-identical is EXACT, not
// near-exact. Conflating them would dock a true 100% match by 1%.
func TestIsNearExact(t *testing.T) {
	tests := []struct {
		name, query, source string
		want                bool
	}{
		{"byte_identical_is_not_near", "Hello world", "Hello world", false},
		{"trailing_period", "Hello world.", "Hello world", true},
		{"case_difference", "hello world", "Hello world", true},
		{"trailing_whitespace", "Hello world ", "Hello world", true},
		{"genuine_word_swap", "This text is in italics", "This text is in bold", false},
		{"internal_punctuation_matters", "Woman, without her man", "Woman without her man", false},
		{"both_empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNearExact(tt.query, tt.source); got != tt.want {
				t.Errorf("IsNearExact(%q, %q) = %v, want %v", tt.query, tt.source, got, tt.want)
			}
		})
	}
}
