package auth

import "fmt"

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
// user back to a users row. Callers substitute "human" when there is no user
// (system / asynq writes).
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
