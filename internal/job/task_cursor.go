package job

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// taskCursor is the keyset position on the Tasks list: the last row's deadline
// and job id. The list sorts by deadline with no deadline last, so a row
// without one sits at 'infinity'.
//
// On the wire it is an opaque "<deadline>:<jobID>" string, where <deadline>
// is unix microseconds (Postgres' timestamp precision, so the round trip is
// exact) or "inf".
type taskCursor struct {
	Deadline pgtype.Timestamptz
	JobID    int64
}

var errBadTaskCursor = errors.New("invalid cursor")

func encodeTaskCursor(deadline pgtype.Timestamptz, jobID int64) string {
	d := "inf"
	if deadline.Valid && deadline.InfinityModifier == pgtype.Finite {
		d = strconv.FormatInt(deadline.Time.UnixMicro(), 10)
	}
	return d + ":" + strconv.FormatInt(jobID, 10)
}

func decodeTaskCursor(s string) (taskCursor, error) {
	d, id, ok := strings.Cut(s, ":")
	if !ok {
		return taskCursor{}, errBadTaskCursor
	}
	jobID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || jobID <= 0 {
		return taskCursor{}, errBadTaskCursor
	}
	if d == "inf" {
		return taskCursor{
			Deadline: pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true},
			JobID:    jobID,
		}, nil
	}
	micros, err := strconv.ParseInt(d, 10, 64)
	if err != nil {
		return taskCursor{}, errBadTaskCursor
	}
	return taskCursor{
		Deadline: pgtype.Timestamptz{Time: time.UnixMicro(micros).UTC(), Valid: true},
		JobID:    jobID,
	}, nil
}
