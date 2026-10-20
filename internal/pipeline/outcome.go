package pipeline

import db "openloc/internal/database/sqlcgen"

// TranslateOutcome reports what a TranslateFile run did, per phase.
//
// A skip reason says why a phase did nothing. Empty means the phase ran (or
// wasn't requested in the first place — callers know what they asked for and
// can interpret accordingly). The pipeline is the sole source of truth for
// phase exercisability; handlers must not duplicate the lookup logic.
//
// The counts say what a phase that ran filled: TMExact segments took a 100% or
// 101% match and were confirmed automatically, TMFuzzy took a lesser or
// penalised match and wait for review, and MTTranslated were machine
// translated by MTEngine, the engine's name as the organization named it.
// TMNames are the memories that filled at least one segment, in the project's
// priority order.
type TranslateOutcome struct {
	TMSkipReason PhaseSkipReason
	MTSkipReason PhaseSkipReason

	TMExact      int
	TMFuzzy      int
	TMNames      []string
	MTTranslated int
	MTEngine     string

	// Held counts the rows the run skipped because a user other than the one
	// who ran it (TriggeredByFromContext) has them open; once per row.
	Held int
}

// heldSet collects the rows the phases skipped as held, so a row both phases
// skip counts once.
type heldSet map[int64]struct{}

// MTMissing is the MT skip reason when the run asked for MT and the project's
// router had no engine able to run it for the target language; empty when MT
// ran, was not set up, was not asked for, or had nothing left to translate.
func (o TranslateOutcome) MTMissing() PhaseSkipReason {
	switch o.MTSkipReason {
	case PhaseSkipNoRoute, PhaseSkipNoProvider:
		return o.MTSkipReason
	}
	return ""
}

// HistoryDetails is what a finished run records in the project history, for
// the phases it asked for: a skipped phase's reason, so "finished TM + MT"
// can't read as if it ran, and a phase that ran its counts.
func (o TranslateOutcome) HistoryDetails(phases TranslatePhases) map[string]any {
	details := map[string]any{}
	switch {
	case o.TMSkipReason != "":
		details["tm_skipped"] = string(o.TMSkipReason)
	case phases.UseTM:
		details["tm_exact"], details["tm_fuzzy"] = o.TMExact, o.TMFuzzy
		details["tm_names"] = append([]string{}, o.TMNames...)
	}
	switch {
	case o.MTSkipReason != "":
		details["mt_skipped"] = string(o.MTSkipReason)
	case phases.UseMT:
		details["mt_translated"], details["mt_engine"] = o.MTTranslated, o.MTEngine
	}
	return details
}

// tmFilled is what a TM phase filled: exact matches, confirmed automatically,
// fuzzy ones, left for review, and the memories that gave them.
type tmFilled struct {
	exact, fuzzy int
	used         map[int64]bool // tm ids that filled at least one segment
}

func (f tmFilled) total() int { return f.exact + f.fuzzy }

// record counts one segment filled from memory tmID.
func (f *tmFilled) record(tmID int64, exact bool) {
	if f.used == nil {
		f.used = map[int64]bool{}
	}
	f.used[tmID] = true
	if exact {
		f.exact++
	} else {
		f.fuzzy++
	}
}

func (f tmFilled) add(g tmFilled) tmFilled {
	out := tmFilled{exact: f.exact + g.exact, fuzzy: f.fuzzy + g.fuzzy, used: map[int64]bool{}}
	for id := range f.used {
		out.used[id] = true
	}
	for id := range g.used {
		out.used[id] = true
	}
	return out
}

// names are the memories that filled at least one segment, in the order of
// assignments (the project's priority order).
func (f tmFilled) names(assignments []db.LookupSettingsTMsRow) []string {
	out := []string{}
	for _, a := range assignments {
		if f.used[a.TmID] {
			out = append(out, a.TmName)
		}
	}
	return out
}

type mtRun struct {
	translated int
	engine     string
}
