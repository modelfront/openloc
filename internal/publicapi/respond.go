package publicapi

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/httputil"
)

// listMaxLimit caps every v1 page; the default page is 50 (httputil.ParseCursor).
const listMaxLimit = 100

// writePage writes one page of a list. rows holds up to limit+1 items — the
// extra one only signals that a next page exists and is not returned. id
// gives the cursor of an item; toDTO maps it to its v1 shape.
func writePage[R, T any](w http.ResponseWriter, rows []R, limit int32, id func(R) int64, toDTO func(R) T) {
	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	var cursor int64
	if hasMore {
		cursor = id(rows[len(rows)-1])
	}
	out := make([]T, len(rows))
	for i, row := range rows {
		out[i] = toDTO(row)
	}
	httputil.Paginated(w, out, cursor, hasMore, -1)
}

// timestamp formats a required timestamp as RFC 3339 in UTC.
func timestamp(t pgtype.Timestamptz) string {
	return t.Time.UTC().Format(time.RFC3339)
}

// optionalTimestamp is timestamp for a nullable column.
func optionalTimestamp(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := timestamp(t)
	return &s
}

// optionalRFC3339 normalizes an already-formatted optional time to UTC RFC
// 3339, the one time format of the v1 contract.
func optionalRFC3339(s *string) *string {
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return s
	}
	out := t.UTC().Format(time.RFC3339)
	return &out
}

// requiredRFC3339 is optionalRFC3339 for a value that is always set.
func requiredRFC3339(s string) string {
	return *optionalRFC3339(&s)
}

// writeAll writes a list that is never paged: every item on one page, with
// the paged list shape so paging can be added without breaking clients.
func writeAll[R, T any](w http.ResponseWriter, rows []R, toDTO func(R) T) {
	out := make([]T, len(rows))
	for i, row := range rows {
		out[i] = toDTO(row)
	}
	httputil.Paginated(w, out, 0, false, -1)
}
