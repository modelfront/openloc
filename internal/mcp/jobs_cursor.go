package mcp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// jobsCursor is a list_jobs page position: the last job's effective deadline
// and its id, the two values the list is sorted on. The next page starts after
// that pair, so a job whose deadline changes between two calls neither hides
// the jobs after it nor repeats the ones before. A job with no deadline sorts
// last; its cursor carries none.
type jobsCursor struct {
	deadline *time.Time
	id       int64
}

const noDeadline = "none"

// String is the cursor as the client passes it back: "<unix µs>.<id>", or
// "none.<id>" after a job with no deadline.
func (c jobsCursor) String() string {
	if c.deadline == nil {
		return fmt.Sprintf("%s.%d", noDeadline, c.id)
	}
	return fmt.Sprintf("%d.%d", c.deadline.UnixMicro(), c.id)
}

// parseJobsCursor reads a cursor from the client; "" is the first page.
func parseJobsCursor(raw string) (jobsCursor, error) {
	if raw == "" {
		return jobsCursor{}, nil
	}
	at, rawID, ok := strings.Cut(raw, ".")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 {
		return jobsCursor{}, invalidf("cursor must be a next_cursor from a previous page")
	}
	if at == noDeadline {
		return jobsCursor{id: id}, nil
	}
	micros, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return jobsCursor{}, invalidf("cursor must be a next_cursor from a previous page")
	}
	deadline := time.UnixMicro(micros).UTC()
	return jobsCursor{deadline: &deadline, id: id}, nil
}

// jobsCursorAfter is the cursor for the page that starts after the job with
// this id and effective deadline (none sorts last).
func jobsCursorAfter(id int64, deadline pgtype.Timestamptz) jobsCursor {
	c := jobsCursor{id: id}
	if deadline.Valid {
		t := deadline.Time
		c.deadline = &t
	}
	return c
}

// afterDeadline is the cursor's deadline for ListMCPJobs: 'infinity' when the
// job has none, which is where those jobs sort.
func (c jobsCursor) afterDeadline() pgtype.Timestamptz {
	if c.deadline == nil {
		return pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}
	}
	return pgtype.Timestamptz{Time: *c.deadline, Valid: true}
}
