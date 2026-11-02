package auth

import (
	"fmt"
	"strings"
)

// UserLabel renders a user identity as a single human-readable string for
// display and for free-text audit columns like translation_memory.updated_by.
//
// Format:
//   - name present and distinct from email → "Name <email>"
//   - name empty or equal to email          → email (avoids "foo@x <foo@x>")
//   - email empty                            → name
//
// The email stays parseable out of the "Name <email>" form (angle brackets),
// which is what GetTMEntryConflictDetails relies on to resolve the editing
// user back to a users row. Callers substitute NoUserLabel when there is no
// user (system / asynq writes).
func UserLabel(name, email string) string {
	switch {
	case email == "":
		return name
	case name == "" || name == email:
		return email
	default:
		return fmt.Sprintf("%s <%s>", name, email)
	}
}

// NoUserLabel stands in for a user in a free-text audit column when no person
// is behind the write: system and asynq writes, and translation_memory's
// created_by for every editor write, where it is also the dedupe key.
const NoUserLabel = "human"

// LabelName is a UserLabel label without its email: the name of
// "Name <email>", and any other label (a TMX creationid, say) as it is unless
// it still holds an "@" — then "", so no form of email gets through.
func LabelName(label string) string {
	label = strings.TrimSpace(label)
	if i := strings.LastIndex(label, " <"); i >= 0 && strings.HasSuffix(label, ">") {
		label = strings.TrimSpace(label[:i])
	}
	if strings.Contains(label, "@") {
		return ""
	}
	return label
}
