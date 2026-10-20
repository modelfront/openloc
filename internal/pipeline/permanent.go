package pipeline

import "errors"

// permanentError is a run failure that every retry would hit again, because
// its cause is configuration the user has to change first (an engine that
// does not translate the pair). The worker fails the job on the first attempt
// instead of holding it in "Retrying…" through asynq's backoff.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as one no retry can fix. The wrapped error stays
// reachable through errors.As, so a ClientError keeps its client-safe message.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err, or an error it wraps, was marked Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}
