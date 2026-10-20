package translationmemory

import (
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
)

// TM match penalties. Every penalty is a
// subtractive deduction from a 0–1 score and they stack, which is more
// predictable than multipliers (a 5% penalty on a 50% match costs 5 points,
// not 2.5).
//
// One number drives everything: Deduction. It sets the displayed percentage and
// it decides auto-confirm — a TM hit is signed off by the pipeline only when
// nothing docked it. That's what makes a zero penalty mean "these are
// equivalent, treat the hit as a true exact match".

// Default penalties, matching the column defaults in migration 20260826090200.
// Used when no settings profile is in hand (tests, and any caller that hasn't
// loaded one yet).
const (
	DefaultSublanguagePenalty = 0.01 // per mismatched side
	DefaultNearExactPenalty   = 0.01 // punctuation/case/whitespace-only difference
)

// Penalties is the per-project penalty configuration. The zero value applies
// nothing — which is a meaningful state ("all penalties disabled"), not a
// missing-config sentinel. Load real values with PenaltiesFromProfile.
type Penalties struct {
	// Sublanguage is charged once per side (source, target) whose language
	// differs from the requested one only by region or script.
	Sublanguage float64
	// NearExact is charged when the source texts differ only in punctuation,
	// case or whitespace.
	NearExact float64
	// PerTM is a flat penalty on every match from a given TM, keyed by tm_id —
	// for client-provided, legacy or otherwise lower-trust memories. Absent
	// means 0.
	PerTM map[int64]float64
}

// MatchInfo is everything about one candidate hit that can attract a penalty.
type MatchInfo struct {
	TMID int64
	// Want* is what the project asked for; Got* is what the TM entry carries.
	WantSrc, GotSrc string
	WantTgt, GotTgt string
	// NearExact: the source texts differ only in punctuation/case/whitespace.
	// Callers get this from IsNearExact.
	NearExact bool
}

// DefaultPenalties returns the built-in defaults (1% sublanguage, 1%
// near-exact, no per-TM penalties).
func DefaultPenalties() Penalties {
	return Penalties{
		Sublanguage: DefaultSublanguagePenalty,
		NearExact:   DefaultNearExactPenalty,
	}
}

// PenaltiesFromProfile converts the stored integer percentages into fractions.
// Per-TM penalties come from the assignments — see WithTMs.
func PenaltiesFromProfile(p db.SettingsProfile) Penalties {
	return Penalties{
		Sublanguage: float64(p.SublanguagePenaltyPct) / 100,
		NearExact:   float64(p.NearExactPenaltyPct) / 100,
	}
}

// WithTMs returns a copy carrying the per-TM penalties from the resolved TM
// assignments. Zero-penalty assignments are omitted — an entry of 0 is the same
// as no entry, and most projects penalise no TM at all.
func (p Penalties) WithTMs(rows []db.LookupSettingsTMsRow) Penalties {
	var perTM map[int64]float64
	for _, r := range rows {
		if r.PenaltyPct == 0 {
			continue
		}
		if perTM == nil {
			perTM = make(map[int64]float64, len(rows))
		}
		perTM[r.TmID] = float64(r.PenaltyPct) / 100
	}
	p.PerTM = perTM
	return p
}

// Deduction totals the penalties this hit attracts. Zero means the hit is
// unimpeachable: full score, and the pipeline may auto-confirm it.
func (p Penalties) Deduction(in MatchInfo) float64 {
	var d float64
	if isSublanguageVariant(in.WantSrc, in.GotSrc) {
		d += p.Sublanguage
	}
	if isSublanguageVariant(in.WantTgt, in.GotTgt) {
		d += p.Sublanguage
	}
	if in.NearExact {
		d += p.NearExact
	}
	return d + p.PerTM[in.TMID]
}

// Apply adjusts a raw similarity score.
//
// A near-exact hit scores from 1.0 rather than from `raw`: it IS the same
// segment to a human, docked for the punctuation difference. Scoring it off the
// raw Levenshtein instead gives a ~5% short-segment cliff — a trailing period
// on a short segment must read 99%, not 95%.
func (p Penalties) Apply(raw float64, in MatchInfo) float64 {
	score := raw
	if in.NearExact {
		score = 1.0
	}
	if score -= p.Deduction(in); score < 0 {
		return 0
	}
	return score
}

// isSublanguageVariant: same language, different region or script. Codes with
// different base languages aren't variants of each other — the lookups can't
// return those anyway, so there's nothing to charge for.
func isSublanguageVariant(want, got string) bool {
	return want != got && langcode.SameBase(want, got)
}
