package spellcheck

import (
	"fmt"

	"openloc/internal/logging"
)

// statusError is a non-200 answer from LanguageTool. The body is kept for the
// error text but never reaches a log: an unrecognised language makes
// LanguageTool echo the full request, which is segment text.
type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("spellcheck: LanguageTool returned %d: %s", e.status, e.body)
}

var _ logging.LogSafe = (*statusError)(nil)

// LogMessage implements logging.LogSafe.
func (e *statusError) LogMessage() string {
	return fmt.Sprintf("spellcheck: LanguageTool returned %d", e.status)
}
