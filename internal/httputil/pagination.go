package httputil

import (
	"net/http"
	"strconv"
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
