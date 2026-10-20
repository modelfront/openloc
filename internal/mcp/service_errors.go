package mcp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"openloc/internal/comments"
	"openloc/internal/hold"
	"openloc/internal/httputil"
	"openloc/internal/job"
	"openloc/internal/project"
	"openloc/internal/settings"
	"openloc/internal/taskedit"
	"openloc/internal/translation"
)

// serviceErr maps an error from a service shared with REST onto the MCP error
// kinds: not-found stays opaque, the typed client messages (a held segment
// names its holder) are written for users and safe to show the model, a
// concurrent edit is errConflict, and anything else is internal (logged,
// reduced to a generic message).
func serviceErr(err error, what string) error {
	if err == nil {
		return nil
	}
	var qaBlock *translation.QABlockedError
	var locked *taskedit.LockedError
	var held *hold.HeldError
	switch {
	case errors.As(err, &locked):
		return invalidf("%s", locked.Message)
	case errors.As(err, &held):
		// The holder is a member of the caller's own organization.
		return invalidf("%s", held.Error())
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, comments.ErrNotFound):
		return errNotFound
	case errors.Is(err, settings.ErrTMProfileLinked):
		return invalidf("%s — detach the project from its TM profile in the app first", settings.ErrTMProfileLinked.Error())
	case errors.Is(err, project.ErrValidation):
		return invalidf("%s", trimValidation(err))
	case errors.As(err, &qaBlock):
		msgs := make([]string, 0, len(qaBlock.Issues))
		ignorable := false
		for _, is := range qaBlock.Issues {
			if is.Severity == "error" {
				msgs = append(msgs, is.Message)
				ignorable = ignorable || is.CheckType != translation.SourceTagsCheckType
			}
		}
		if !ignorable {
			// A tag the source does not have is not a stored finding.
			return invalidf("QA errors block confirming this segment: %s. Remove the tag; this check cannot be ignored.", strings.Join(msgs, "; "))
		}
		return invalidf("QA errors block confirming this segment: %s. Fix the text, or ignore a false positive with openloc_ignore_qa_finding.", strings.Join(msgs, "; "))
	case errors.Is(err, translation.ErrLocked):
		return invalidf("segment is locked — it must be unlocked first")
	case errors.Is(err, translation.ErrNotFirstStep), errors.Is(err, job.ErrRunInterrupted):
		return invalidf("%s", err.Error())
	case errors.Is(err, translation.ErrConflict):
		return errConflict
	}
	for _, as := range []func(error) (string, bool){httputil.AsClientError, httputil.AsConflictError, httputil.AsForbiddenError} {
		if msg, ok := as(err); ok {
			return invalidf("%s", msg)
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}
