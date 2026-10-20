package testutil

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
)

// SetUserName gives a seeded user a display name. SeedUser names every user
// after their email; an empty name is a user with no name at all.
func SetUserName(t testing.TB, q db.Querier, userID int64, name string) {
	t.Helper()
	if _, err := q.UpdateUserProfile(context.Background(), db.UpdateUserProfileParams{ID: userID, Name: name}); err != nil {
		t.Fatalf("SetUserName(%d, %q): %v", userID, name, err)
	}
}
