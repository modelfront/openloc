package segmentedit

import "net/http"

// Error is a split or join the service refuses, with the HTTP status and the
// code the editor branches on.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// StatusCode and ErrorCode let httputil.WriteCodedError write the refusal.
func (e *Error) StatusCode() int   { return e.Status }
func (e *Error) ErrorCode() string { return e.Code }

var (
	ErrSplitPosition = &Error{http.StatusBadRequest, "split_position",
		"The segment cannot be split here. Place the cursor between two words, outside a tag pair."}
	ErrSplitUnsupported = &Error{http.StatusBadRequest, "split_unsupported",
		"This segment cannot be split."}
	ErrSplitTags = &Error{http.StatusBadRequest, "split_tags",
		"This segment's tags cannot be split."}
	ErrJoinCount = &Error{http.StatusBadRequest, "join_count",
		"Select at least two rows to join."}
	ErrNotAdjacent = &Error{http.StatusBadRequest, "join_not_adjacent",
		"Only rows that follow each other can be joined."}
	ErrOtherUnit = &Error{http.StatusBadRequest, "join_other_unit",
		"Only sentences of one paragraph, or the pieces of one split segment, can be joined."}
	ErrJoinPartial = &Error{http.StatusBadRequest, "join_partial",
		"Join every piece of a split segment, or join its pieces back first."}
	ErrJoinUnsafe = &Error{http.StatusBadRequest, "join_unsafe",
		"These sentences cannot be joined in this file."}
	ErrLocked = &Error{http.StatusConflict, "segment_locked",
		"A locked segment cannot be split or joined. Unlock it first."}
	ErrInFlight = &Error{http.StatusConflict, "segment_in_flight",
		"The segment is being pre-translated. Try again when it is done."}
	ErrPipelineRunning = &Error{http.StatusConflict, "pipeline_running",
		"Pre-translation is running on this job. Try again when it is done."}
	ErrConflict = &Error{http.StatusConflict, "conflict",
		"The segment changed since you opened it. Reload and try again."}
)
