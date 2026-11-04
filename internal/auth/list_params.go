package auth

import (
	"errors"
	"net/http"
	"strings"

	"openloc/internal/httputil"
	"openloc/internal/langcode"
)

// listQuery is what a settings list (Members, Vendors) reads from its URL:
// the page as a row offset, the search text, and a language pair.
type listQuery struct {
	Page   httputil.CursorParams
	Q      *string
	Source *string
	Target *string
}

// Off is the page's row offset, checked to fit the query's int32.
func (l listQuery) Off() int32 { return int32(l.Page.Cursor) }

// parseListQuery reads cursor, limit, q, source and target. The error is a
// user-facing 400 message.
func parseListQuery(r *http.Request, maxLimit int32) (listQuery, error) {
	page, err := httputil.ParseOffsetPage(r, maxLimit)
	l := listQuery{Page: page.CursorParams, Q: page.Q}
	if err != nil {
		return l, err
	}
	query := r.URL.Query()
	if s := strings.TrimSpace(query.Get("source")); s != "" {
		code := langcode.Normalize(s)
		l.Source = &code
	}
	if s := strings.TrimSpace(query.Get("target")); s != "" {
		code := langcode.Normalize(s)
		l.Target = &code
	}
	return l, nil
}

// parseRoleFilter reads the role filter; an unknown role is a user-facing 400
// message.
func parseRoleFilter(r *http.Request) (*string, error) {
	role := r.URL.Query().Get("role")
	if role == "" {
		return nil, nil
	}
	if roleRank[role] == 0 {
		return nil, errors.New("unknown role")
	}
	return &role, nil
}
