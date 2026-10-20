package mt

import (
	"fmt"

	"openloc/internal/logging"
)

// apiError is a non-2xx answer from a vendor's translate endpoint. The body is
// kept for the error text, but never reaches a log or an errtrack notice: a
// vendor can echo the request back, and the request is segment text.
type apiError struct {
	vendor string
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s returned %d: %s", e.vendor, e.status, e.body)
}

var _ logging.LogSafe = (*apiError)(nil)

// LogMessage implements logging.LogSafe.
func (e *apiError) LogMessage() string { return fmt.Sprintf("%s returned %d", e.vendor, e.status) }
