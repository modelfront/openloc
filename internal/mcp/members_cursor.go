package mcp

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// membersCursor is a list_members page position: the last member's name and
// id, the two values the list is sorted on. The cursor carries both, so the
// next page does not depend on that member still being in the organization.
type membersCursor struct {
	name string
	id   int64
}

// String is the cursor as the client passes it back: "<base64url name>.<id>".
// The name is encoded because it may hold any character, the dot included.
func (c membersCursor) String() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.name)) + "." + strconv.FormatInt(c.id, 10)
}

// parseMembersCursor reads a cursor from the client; "" is the first page.
func parseMembersCursor(raw string) (membersCursor, error) {
	if raw == "" {
		return membersCursor{}, nil
	}
	rawName, rawID, ok := strings.Cut(raw, ".")
	name, nameErr := base64.RawURLEncoding.DecodeString(rawName)
	id, idErr := strconv.ParseInt(rawID, 10, 64)
	if !ok || nameErr != nil || idErr != nil || id <= 0 {
		return membersCursor{}, invalidf("cursor must be a next_cursor from a previous page")
	}
	return membersCursor{name: string(name), id: id}, nil
}
