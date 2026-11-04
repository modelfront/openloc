package httputil

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// CursorParams holds parsed cursor pagination parameters.
type CursorParams struct {
	Cursor int64
	Limit  int32
}

// ParseCursor extracts cursor pagination params from the request query string.
// maxLimit caps the limit; pass 0 to use the default cap of 100.
func ParseCursor(r *http.Request, maxLimit int32) CursorParams {
	if maxLimit == 0 {
		maxLimit = 100
	}
	defaultLimit := int32(50)
	if defaultLimit > maxLimit {
		defaultLimit = maxLimit
	}

	p := CursorParams{Limit: defaultLimit}

	if c := r.URL.Query().Get("cursor"); c != "" {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n >= 0 {
			p.Cursor = n
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 32); err == nil && n > 0 && int32(n) <= maxLimit {
			p.Limit = int32(n)
		}
	}
	return p
}

// Paginated writes a cursor-paginated JSON response.
// hasMore indicates whether more items exist beyond this page.
// nextCursor is the cursor for the next page (typically the last item's ID).
// Pass total < 0 to omit the "total" field from the response.
func Paginated(w http.ResponseWriter, items any, nextCursor int64, hasMore bool, total int64) {
	resp := map[string]any{
		"data":     items,
		"cursor":   nextCursor,
		"has_more": hasMore,
	}
	if total >= 0 {
		resp["total"] = total
	}
	JSON(w, http.StatusOK, resp)
}

// WholeFileCap caps the routes that return every row of a file in one
// response. A larger file is read page by page through the CAT editor routes
// (/api/jobs/{jobID}/editor/*).
const WholeFileCap = 5000

// RefuseWholeFile answers 422 when a whole-file list came back with more than
// WholeFileCap rows; the query reads one row past the cap to tell.
func RefuseWholeFile(w http.ResponseWriter, rows int) bool {
	if rows <= WholeFileCap {
		return false
	}
	ErrorCode(w, http.StatusUnprocessableEntity, "file_too_large",
		fmt.Sprintf("the file has more than %d segments; read it page by page", WholeFileCap))
	return true
}

// ParseSearch reads a list's search text from `q`: trimmed, nil when empty.
// Longer than max is an error with a user-facing message.
func ParseSearch(r *http.Request, max int) (*string, error) {
	s := strings.TrimSpace(r.URL.Query().Get("q"))
	if s == "" {
		return nil, nil
	}
	if len(s) > max {
		return nil, errors.New("search text is too long")
	}
	return &s, nil
}

// MaxSearch caps a list's search text; a name or an email is shorter.
const MaxSearch = 200

// OffsetPage is one page of a list read by row offset: the cursor is the
// offset, checked to fit the query's int32, and Q the search text.
type OffsetPage struct {
	CursorParams
	Q *string
}

// Off is the page's row offset.
func (p OffsetPage) Off() int32 { return int32(p.Cursor) }

// ParseOffsetPage reads cursor, limit and q. The error is a user-facing 400
// message.
func ParseOffsetPage(r *http.Request, maxLimit int32) (OffsetPage, error) {
	p := OffsetPage{CursorParams: ParseCursor(r, maxLimit)}
	if p.Cursor > math.MaxInt32 {
		return p, errors.New("invalid cursor")
	}
	var err error
	p.Q, err = ParseSearch(r, MaxSearch)
	return p, err
}

// PageTotal is a page's "total" field: every row of the page carries the
// count of all matches; a page past the end has no row to read it from, so
// the field is left out (-1) there.
func PageTotal(cursor int64, rows int, firstRowTotal int64) int64 {
	if rows > 0 {
		return firstRowTotal
	}
	if cursor == 0 {
		return 0
	}
	return -1
}
