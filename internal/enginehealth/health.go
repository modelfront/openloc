// Package enginehealth answers one question in one place: can this engine
// translate right now?
//
// Two things stop it, and they behave identically everywhere — on the engine
// list, on every router that routes to it, and at translate time:
//
//   - it is switched off, or
//   - the AI settings key it spends has been deleted.
//
// Deleting a key does not touch its engines: the FK nulls the reference, the
// engine keeps its name, options and routes, and this verdict is what marks it
// broken.
//
// Deliberately its own package, not a method on a DTO: the MT handler, the AI
// engine handler, both router surfaces and the pipeline all need the same
// verdict, and four copies of "disabled or no token" is how two of them end up
// disagreeing about whether a job should run.
package enginehealth

import db "openloc/internal/database/sqlcgen"

// Reason is why an engine cannot translate; "" means it can.
type Reason string

const (
	// OK is the usable state. Empty so a caller can test the zero value.
	OK Reason = ""
	// Disabled — someone switched it off. Reversible from the engine's editor.
	Disabled Reason = "disabled"
	// NoKey — the AI settings key it spent was deleted. Fixed by pointing the
	// engine at another key, not by re-adding the old one (a new key is a new
	// row, so the engine still has to be edited).
	NoKey Reason = "no_key"
	// NoExtension — the engine's key belongs to an extension that was removed.
	// Fixed by installing the extension again.
	NoExtension Reason = "no_extension"
)

// Pages where an engine's key is chosen, for the no-key sentence.
const (
	PageMT = "MT → Engines"
	PageAI = "AI → Engines"
)

// Message is the one client-safe sentence for a reason. page is where the
// engine is edited (PageMT, PageAI); "" leaves the place out.
func (r Reason) Message(engineName, page string) string {
	switch r {
	case Disabled:
		return engineName + " is switched off"
	case NoKey:
		if page == "" {
			return `the engine "` + engineName + `" has no API key. Choose a key for it in the engine's settings.`
		}
		return `the engine "` + engineName + `" has no API key. Choose a key for it in ` + page + `.`
	case NoExtension:
		return `the engine "` + engineName + `" uses a custom provider that is no longer added. Add it again in AI settings → Providers.`
	default:
		return ""
	}
}

// Usable reports whether an engine can translate.
func (r Reason) Usable() bool { return r == OK }

// Of resolves an engine's state from the two facts that decide it.
//
// `needsKey` is what separates an MT engine that authenticates with a key from
// a deployment-supplied one that needs none: a LibreTranslate engine with no
// token is correct, not broken.
func Of(enabled, needsKey, hasKey bool) Reason {
	if !enabled {
		return Disabled
	}
	if needsKey && !hasKey {
		return NoKey
	}
	return OK
}

// Lookup answers "can this engine translate?" for a whole tenant at once, so a
// handler assembling many routers asks the database twice rather than per rule.
//
// Both kinds live in it because an MT route may point at either a classic MT
// engine or an LLM one, and a caller walking a router's rules should
// not have to care which it found.
type Lookup struct {
	mt map[int64]Reason
	ai map[int64]Reason
}

// MT reports an mt_engines row's state. An id this tenant does not own answers
// OK: a router cannot reference it, so calling it broken would be a warning
// nobody could act on.
func (l Lookup) MT(id int64) Reason { return l.mt[id] }

// AI reports an ai_engines row's state, same rule.
func (l Lookup) AI(id int64) Reason { return l.ai[id] }

// NewLookup builds the verdicts from one tenant's engines.
//
// `needsKey` is injected rather than hardcoded so this package keeps no opinion
// about MT engine types — `mtengine.NeedsToken` owns that list, and a second
// copy here is how the two would drift.
func NewLookup(
	mtEngines []db.MtEngine,
	aiEngines []db.AiEngine,
	aiRows []db.AiEngineRow,
	needsKey func(engineType string) bool,
) Lookup {
	out := Lookup{mt: map[int64]Reason{}, ai: map[int64]Reason{}}
	// A caller with no MT engines to classify (the AI router) may pass nil for
	// both. Guarded rather than assumed: the loop below not running today is a
	// property of that caller's data, not of this function.
	if needsKey == nil {
		needsKey = func(string) bool { return false }
	}
	for _, e := range mtEngines {
		out.mt[e.ID] = Of(e.Enabled, needsKey(e.EngineType), e.TokenID != nil)
	}
	// An AI engine authenticates through its rows, and a row's token is
	// required at save — so a null one means the key was deleted. An engine
	// with no rows at all cannot run either.
	hasKey := map[int64]bool{}
	for _, r := range aiRows {
		if _, seen := hasKey[r.EngineID]; !seen {
			hasKey[r.EngineID] = true
		}
		if r.TokenID == nil {
			hasKey[r.EngineID] = false
		}
	}
	for _, e := range aiEngines {
		out.ai[e.ID] = Of(e.Enabled, true, hasKey[e.ID])
	}
	return out
}

// Shown is how a page presents r. The demo organization holds no keys and
// runs nothing, so a missing key is no problem to show
// there; everywhere else r is shown as it is.
func Shown(r Reason, demo bool) Reason {
	if demo && r == NoKey {
		return OK
	}
	return r
}

// Shown is the lookup as a page presents it (see Shown).
func (l Lookup) Shown(demo bool) Lookup {
	if !demo {
		return l
	}
	out := Lookup{mt: make(map[int64]Reason, len(l.mt)), ai: make(map[int64]Reason, len(l.ai))}
	for id, r := range l.mt {
		out.mt[id] = Shown(r, true)
	}
	for id, r := range l.ai {
		out.ai[id] = Shown(r, true)
	}
	return out
}

// Worst is the reason a ROUTER should report across the engines it routes to.
//
// NoKey outranks Disabled: a router with both problems leads with the broken
// one, because switching an engine back on is a click and repointing it is not.
func Worst(reasons ...Reason) Reason {
	worst := OK
	for _, r := range reasons {
		if r == NoKey {
			return NoKey
		}
		if r == Disabled {
			worst = Disabled
		}
	}
	return worst
}
