package issues

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Cursor is a list page's position: the last issue's last update and id. The
// next page starts after that pair, so an issue that changes between two
// pages neither repeats nor hides the rest of the list.
type Cursor struct {
	At time.Time
	ID int64
}

// String is the cursor as the client passes it back: "<unix µs>.<id>", or
// "" for the first page.
func (c Cursor) String() string {
	if c.ID == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d", c.At.UnixMicro(), c.ID)
}

// ParseCursor reads a cursor from the client; "" is the first page.
func ParseCursor(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}
	at, id, ok := strings.Cut(raw, ".")
	micros, errAt := strconv.ParseInt(at, 10, 64)
	n, errID := strconv.ParseInt(id, 10, 64)
	if !ok || errAt != nil || errID != nil || n <= 0 {
		return Cursor{}, clientError("Invalid cursor.")
	}
	return Cursor{At: time.UnixMicro(micros), ID: n}, nil
}

// at is the cursor's time for the list queries; NULL on the first page.
func (c Cursor) at() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: c.At, Valid: c.ID != 0}
}
