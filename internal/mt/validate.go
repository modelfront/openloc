package mt

import (
	"context"
	"fmt"
)

// AssignmentValidation answers "can this engine take this pair?".
type AssignmentValidation struct {
	// Rejected is true when the engine's own catalog does not cover the pair,
	// not even after base-code fallback.
	Rejected bool

	// Reason names which side failed and why, for the log behind the client
	// error. Empty unless Rejected.
	Reason string
}

// ValidateAssignment reports whether an engine covers a language pair, for the
// check the pipeline makes before it sends anything. It never refuses on a
// compiled-in table — see supportedCatalog.
//
// Which code the engine is actually sent is ResolveLocalePair's answer, not
// this one.
func ValidateAssignment(ctx context.Context, sourceLang, targetLang string, engine Engine) AssignmentValidation {
	cat, fromEngine := supportedCatalog(ctx, engine)
	if !fromEngine || cat.Empty() {
		// A compiled-in table is our guess, not the engine's word: an LLM
		// translation engine publishes no catalog at all, and a vendor we cannot
		// reach has not refused anything. Resolve against it, never refuse on it.
		return AssignmentValidation{}
	}

	source, ok := resolveCode(codeSet(cat.Sources), sourceLang)
	if !ok {
		return AssignmentValidation{
			Rejected: true,
			Reason:   fmt.Sprintf("source language %q is not in the engine's catalog", sourceLang),
		}
	}

	targets := targetSet(cat, source)
	if targets == nil {
		return AssignmentValidation{
			Rejected: true,
			Reason:   fmt.Sprintf("source language %q lists no targets in the engine's catalog", sourceLang),
		}
	}

	// A base-code fallback is not a rejection: the pair still translates, and
	// the swap is already on the record from logResolved.
	if _, ok := resolveCode(targets, targetLang); !ok {
		return AssignmentValidation{
			Rejected: true,
			Reason:   fmt.Sprintf("target language %q is not in the engine's catalog, and has no base code there either", targetLang),
		}
	}
	return AssignmentValidation{}
}

// ValidateTarget reports whether an engine writes a target language. An MT
// router routes by target alone and carries no source, so this is the check
// available when a rule is saved, before any job exists.
//
// Like ValidateAssignment it refuses only on the engine's own catalog: a
// compiled-in stand-in is our guess, and a save-time rejection blocks work
// rather than a doomed phase.
func ValidateTarget(ctx context.Context, targetLang string, engine Engine) AssignmentValidation {
	cat, fromEngine := supportedCatalog(ctx, engine)
	if !fromEngine || cat.Empty() {
		return AssignmentValidation{}
	}
	if _, ok := resolveCode(codeSet(cat.Targets), targetLang); !ok {
		return AssignmentValidation{
			Rejected: true,
			Reason:   fmt.Sprintf("does not translate into %s", targetLang),
		}
	}
	return AssignmentValidation{}
}
