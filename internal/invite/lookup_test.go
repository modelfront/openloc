package invite_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Public invite-token lookup for the accept page.

func seedLookupInvite(t *testing.T, q db.Querier, tenantID, invitedBy int64, email string, expiresIn time.Duration) db.Invite {
	t.Helper()
	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	inv, err := q.CreateInvite(context.Background(), db.CreateInviteParams{
		TenantID:  tenantID,
		Email:     email,
		Role:      "linguist",
		InvitedBy: invitedBy,
		Token:     token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
	})
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	return inv
}

func TestIntegrationInviteLookup_Valid(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Lookup Corp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	// New identity: user_exists=false drives the set-password form.
	inv := seedLookupInvite(t, q, tenant.ID, owner.ID, "newbie@test.com", 7*24*time.Hour)
	rec := testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+inv.Token, nil, 0, 0)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var resp struct {
		Data struct {
			Email       string `json:"email"`
			OrgName     string `json:"org_name"`
			Role        string `json:"role"`
			UserExists  bool   `json:"user_exists"`
			HasPassword bool   `json:"has_password"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Email != "newbie@test.com" || resp.Data.OrgName != "Lookup Corp" || resp.Data.Role != "linguist" {
		t.Fatalf("unexpected payload: %+v", resp.Data)
	}
	if resp.Data.UserExists || resp.Data.HasPassword {
		t.Fatalf("fresh email must report user_exists=false, got %+v", resp.Data)
	}

	// Existing identity with a password: drives the "sign in to accept" CTA.
	inv2 := seedLookupInvite(t, q, tenant.ID, owner.ID, "owner@test.com", 7*24*time.Hour)
	rec = testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+inv2.Token, nil, 0, 0)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.DecodeJSON(t, rec, &resp)
	if !resp.Data.UserExists || !resp.Data.HasPassword {
		t.Fatalf("existing password account must report user_exists+has_password, got %+v", resp.Data)
	}
}

// TestIntegrationInviteLookup_FlatNotFound: unknown, expired, accepted, and revoked
// tokens are indistinguishable 404s — the endpoint is not a token oracle.
func TestIntegrationInviteLookup_FlatNotFound(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "Oracle2 Corp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	expired := seedLookupInvite(t, q, tenant.ID, owner.ID, "a@test.com", -time.Hour)
	accepted := seedLookupInvite(t, q, tenant.ID, owner.ID, "b@test.com", 7*24*time.Hour)
	if _, err := q.AcceptInvite(context.Background(), accepted.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	revoked := seedLookupInvite(t, q, tenant.ID, owner.ID, "c@test.com", 7*24*time.Hour)
	if err := q.DeleteInvite(context.Background(), db.DeleteInviteParams{ID: revoked.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	for name, token := range map[string]string{
		"unknown":  "nope-not-real",
		"expired":  expired.Token,
		"accepted": accepted.Token,
		"revoked":  revoked.Token,
	} {
		rec := testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+token, nil, 0, 0)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected flat 404, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}
