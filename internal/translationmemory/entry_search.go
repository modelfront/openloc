package translationmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	db "openloc/internal/database/sqlcgen"
)

// Search scopes for EntryFilter.Scope. "" means ScopeBoth.
const (
	ScopeSource = "source"
	ScopeTarget = "target"
	ScopeBoth   = "both"
)

// Limits on an entry search. A regex search runs a user-written pattern over
// every entry of the TM, so one search must not hold a database connection for
// long, nor ship an unbounded pattern to Postgres.
const (
	defaultSearchTimeout = 5 * time.Second
	maxSearchRunes       = 1000
)

// pgInvalidRegex is SQLSTATE 2201B, raised for a pattern Postgres cannot
// compile or finds too complex.
const pgInvalidRegex = "2201B"

// searchScope reports which fields a search tests.
func searchScope(scope string) (inSource, inTarget bool, err error) {
	switch scope {
	case "", ScopeBoth:
		return true, true, nil
	case ScopeSource:
		return true, false, nil
	case ScopeTarget:
		return false, true, nil
	}
	return false, false, invalid("scope must be 'source', 'target' or 'both'")
}

// searchPattern is the Postgres regular expression the search queries test
// each field against. Plain text is escaped, so a substring search and a
// regex search run through the same SQL; Exact anchors it to the whole field.
func searchPattern(f EntryFilter) string {
	p := escapeRegex(f.Query)
	if f.Regex {
		p = "(?:" + jsRegexToPostgres(f.Query) + ")"
	}
	if f.Exact {
		p = "^" + p + "$"
	}
	return p
}

// escapeRegex escapes every regex metacharacter. In a Postgres ARE a
// backslash before a non-alphanumeric character always means that character.
func escapeRegex(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\^$.|?*+()[]{}`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// jsRegexToPostgres maps the word-boundary escapes of the JavaScript syntax
// the browser validates and highlights with onto Postgres ARE: `\b` and `\B`
// are `\y` and `\Y` there, while a Postgres `\b` is a backspace and would
// silently match nothing. Inside a bracket expression `\b` is a backspace in
// both, so it is left alone.
func jsRegexToPostgres(p string) string {
	var b strings.Builder
	inBracket := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' && i+1 < len(p):
			next := p[i+1]
			switch {
			case next == 'b' && !inBracket:
				b.WriteString(`\y`)
			case next == 'B' && !inBracket:
				b.WriteString(`\Y`)
			default:
				b.WriteByte(c)
				b.WriteByte(next)
			}
			i++
		case c == '[' && !inBracket:
			inBracket = true
			b.WriteByte(c)
		case c == ']' && inBracket:
			inBracket = false
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// checkSearchLength rejects a query longer than maxSearchRunes.
func checkSearchLength(q string) error {
	if utf8.RuneCountInString(q) > maxSearchRunes {
		return invalid("The search text is too long. Use at most %d characters.", maxSearchRunes)
	}
	return nil
}

// ranOutOfTime reports whether limitCtx hit its own deadline while the
// caller's ctx was still live — the search's limit, not the caller's.
func ranOutOfTime(ctx, limitCtx context.Context) bool {
	return ctx.Err() == nil && errors.Is(limitCtx.Err(), context.DeadlineExceeded)
}

// searchError turns the failures that are the user's to fix into a
// ValidationError: a pattern Postgres rejects, and a search that ran past
// the time limit of searchCtx. A cancel or deadline of the caller (ctx) stays
// an error.
func searchError(ctx, searchCtx context.Context, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgInvalidRegex {
		return invalid("This regular expression is not valid.")
	}
	if ranOutOfTime(ctx, searchCtx) {
		return invalid("This search took too long. Choose a language first to search fewer entries.")
	}
	return err
}

// countSearch counts a search's matches within countTimeout. Past the limit
// the total is UnknownTotal, so a slow count never fails the search itself.
func (s *Service) countSearch(ctx context.Context, p db.CountTMEntriesSearchParams) (int64, error) {
	countCtx, cancel := context.WithTimeout(ctx, s.countTimeout)
	defer cancel()
	n, err := s.queries.CountTMEntriesSearch(countCtx, p)
	switch {
	case err == nil:
		return n, nil
	case ranOutOfTime(ctx, countCtx):
		return UnknownTotal, nil
	default:
		return 0, fmt.Errorf("count TM search: %w", searchError(ctx, countCtx, err))
	}
}
