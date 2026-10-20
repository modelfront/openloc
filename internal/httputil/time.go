package httputil

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ParseDeadline converts an optional ISO 8601 (RFC3339) string into a
// pgtype.Timestamptz. A nil or empty pointer means "no deadline" → NULL
// (Valid=false). A malformed non-empty value returns ok=false so the caller
// can respond 400 rather than silently dropping the value.
func ParseDeadline(s *string) (d pgtype.Timestamptz, ok bool) {
	if s == nil || *s == "" {
		return pgtype.Timestamptz{}, true
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return pgtype.Timestamptz{}, false
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, true
}
