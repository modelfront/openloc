package httputil

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) — lets a handler translate it into a clean 409
// instead of an opaque 500.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsUniqueViolationOn reports a unique violation on one named index or
// constraint, for an insert that expects exactly that clash.
func IsUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// IsForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (SQLSTATE 23503): the row a write points at is gone.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// DBError handles the common pgx.ErrNoRows → 404 pattern.
// resource is the noun for the error message (e.g., "project", "TM").
//
//	if httputil.DBError(w, err, "project") { return }
//
// Returns true if an error was written (caller should return).
//   - nil → returns false (no error)
//   - pgx.ErrNoRows → 404 "resource not found"
//   - any other error → 500 "failed to get resource"
func DBError(w http.ResponseWriter, err error, resource string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, resource+" not found")
		return true
	}
	recordError(w, err)
	Error(w, http.StatusInternalServerError, "failed to get "+resource)
	return true
}

// ClientError carries a client-safe 400 message through a call stack that
// otherwise collapses to an opaque 500. Business-logic layers (e.g. settings
// validation) return NewClientError; handlers unwrap it with AsClientError.
type ClientError struct{ Message string }

func (e *ClientError) Error() string { return e.Message }

// NewClientError wraps a client-safe validation message.
func NewClientError(message string) error { return &ClientError{Message: message} }

// AsClientError reports whether err (or a wrapped error) is a ClientError and
// returns its client-safe message.
func AsClientError(err error) (string, bool) {
	var ce *ClientError
	if errors.As(err, &ce) {
		return ce.Message, true
	}
	return "", false
}

// ConflictError is ClientError's 409 sibling: the request is well-formed but
// the current state forbids it (e.g. a workflow edit that would move a step a
// job already reached). Handlers unwrap it with AsConflictError.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

// NewConflictError wraps a client-safe 409 message.
func NewConflictError(message string) error { return &ConflictError{Message: message} }

// AsConflictError reports whether err (or a wrapped error) is a ConflictError
// and returns its client-safe message.
func AsConflictError(err error) (string, bool) {
	var ce *ConflictError
	if errors.As(err, &ce) {
		return ce.Message, true
	}
	return "", false
}

// ForbiddenError is ClientError's 403 sibling: the caller is authenticated
// but this action on this resource is not theirs to take. Handlers unwrap it
// with AsForbiddenError.
type ForbiddenError struct{ Message string }

func (e *ForbiddenError) Error() string { return e.Message }

// NewForbiddenError wraps a client-safe 403 message.
func NewForbiddenError(message string) error { return &ForbiddenError{Message: message} }

// AsForbiddenError reports whether err (or a wrapped error) is a
// ForbiddenError and returns its client-safe message.
func AsForbiddenError(err error) (string, bool) {
	var fe *ForbiddenError
	if errors.As(err, &fe) {
		return fe.Message, true
	}
	return "", false
}

// ServiceError writes the response for an error from a domain service:
// pgx.ErrNoRows → 404 "<resource> not found", ClientError → 400,
// ForbiddenError → 403, ConflictError → 409, anything else → 500
// "failed to <verb> <resource>". Returns true when err was non-nil.
func ServiceError(w http.ResponseWriter, err error, verb, resource string) bool {
	if err == nil {
		return false
	}
	if WriteCodedError(w, err) {
		return true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		NotFound(w, resource)
		return true
	}
	if msg, ok := AsClientError(err); ok {
		BadRequest(w, msg)
		return true
	}
	if msg, ok := AsForbiddenError(err); ok {
		Error(w, http.StatusForbidden, msg)
		return true
	}
	if msg, ok := AsConflictError(err); ok {
		Error(w, http.StatusConflict, msg)
		return true
	}
	recordError(w, err)
	ServerError(w, verb, resource)
	return true
}

// CodedError is a service error that carries its own HTTP status and the
// machine-readable code the frontend branches on (taskedit's refusals).
type CodedError interface {
	error
	StatusCode() int
	ErrorCode() string
}

// WriteCodedError writes err with its own status and code when it (or a
// wrapped error) is a CodedError, and reports whether it did.
func WriteCodedError(w http.ResponseWriter, err error) bool {
	var ce CodedError
	if !errors.As(err, &ce) {
		return false
	}
	ErrorCode(w, ce.StatusCode(), ce.ErrorCode(), ce.Error())
	return true
}

// ErrorCode writes an error with a machine-readable `code` the frontend can
// branch on (e.g. "free_email", "domain_taken", "org_pending"), rather than the
// generic http.StatusText that plain Error() uses. Same envelope shape:
// {"error":{"code":..., "message":...}}.
func ErrorCode(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

// BadRequest writes a 400 error with the given message.
func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, message)
}

// NotFound writes a 404 error: "resource not found".
func NotFound(w http.ResponseWriter, resource string) {
	Error(w, http.StatusNotFound, resource+" not found")
}

// ServerError writes a 500 error: "failed to verb resource".
func ServerError(w http.ResponseWriter, verb, resource string) {
	Error(w, http.StatusInternalServerError, "failed to "+verb+" "+resource)
}
