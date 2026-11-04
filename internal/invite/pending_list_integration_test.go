package invite_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"openloc/internal/testutil"
)

// TestIntegrationListPendingInvites: ?status=pending returns only the invites
// nobody accepted yet, so the Members tab does not load the whole invite
// history to show them; a status the server does not know is a 400.
func TestIntegrationListPendingInvites(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)

	org := testutil.SeedTenant(t, q, "Invite Org")
	owner := testutil.SeedUser(t, q, org.ID, "owner@invites.test", "pw")
	pending := seedInvite(t, q, org.ID, owner.ID, "pending@team.test", "linguist")
	accepted := seedInvite(t, q, org.ID, owner.ID, "joined@team.test", "linguist")
	if _, err := pool.Exec(ctx, `UPDATE invites SET accepted_at = $1 WHERE id = $2`, time.Now(), accepted.ID); err != nil {
		t.Fatal(err)
	}

	get := func(query string) []int64 {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/invites"+query, nil, owner.ID, org.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var body struct {
			Data []struct{ ID int64 } `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &body)
		ids := []int64{}
		for _, i := range body.Data {
			ids = append(ids, i.ID)
		}
		return ids
	}
	if got := get(""); len(got) != 2 {
		t.Errorf("all invites = %v, want both", got)
	}
	if got := get("?status=pending"); len(got) != 1 || got[0] != pending.ID {
		t.Errorf("pending invites = %v, want only %d", got, pending.ID)
	}
	rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/invites?status=bogus", nil, owner.ID, org.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}
