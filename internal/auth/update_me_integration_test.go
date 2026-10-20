package auth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationUpdateMeRefusesEmailChange: email-matched invites and domain
// auto-join trust that an account's email belongs to its user, so the email
// can't be changed. Otherwise anyone could claim an unregistered address and
// collect its pending invites.
func TestIntegrationUpdateMeRefusesEmailChange(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	home := testutil.SeedTenant(t, q, "HomeCorp")
	user := testutil.SeedUser(t, q, home.ID, "bob@test.com", "pass123")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	otherOwner := testutil.SeedUser(t, q, other.ID, "boss@other.test", "pass123")
	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := q.CreateInvite(ctx, db.CreateInviteParams{
		TenantID: other.ID, Email: "newcomer@other.test", Role: "admin", InvitedBy: otherOwner.ID, Token: token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	rec := testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"email": "newcomer@other.test"}, user.ID, home.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if got, err := q.GetUser(ctx, user.ID); err != nil || got.Email != "bob@test.com" {
		t.Fatalf("email after a refused change = %q (err %v)", got.Email, err)
	}
	// Listing orgs materializes email-matched invites.
	rec = testutil.DoRequest(t, router, "GET", "/api/auth/orgs", nil, user.ID, home.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if _, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: user.ID, TenantID: other.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("user joined the other org through its invite: %v", err)
	}

	// The current address in any case and spacing is a no-op; the name still changes.
	rec = testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"name": "Bob B", "email": " BOB@test.com "}, user.ID, home.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got, err := q.GetUser(ctx, user.ID); err != nil || got.Name != "Bob B" || got.Email != "bob@test.com" {
		t.Fatalf("user after a name change = %+v (err %v)", got, err)
	}
}

// TestIntegrationUpdateMeTrimsAndRequiresName: Settings sends only the name.
// It is saved trimmed, and an empty one or one past 200 characters is refused,
// as the form requires.
func TestIntegrationUpdateMeTrimsAndRequiresName(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NameCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "bob@test.com", "pass123")

	rec := testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"name": "  Bob Builder  "}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if got, err := q.GetUser(ctx, user.ID); err != nil || got.Name != "Bob Builder" {
		t.Fatalf("name after a padded save = %q (err %v), want %q", got.Name, err, "Bob Builder")
	}

	rec = testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"name": "   "}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if got, err := q.GetUser(ctx, user.ID); err != nil || got.Name != "Bob Builder" {
		t.Fatalf("an empty name was saved: %q (err %v)", got.Name, err)
	}

	// The cap counts characters: 200 two-byte letters fit.
	rec = testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"name": strings.Repeat("é", 201)}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if got, err := q.GetUser(ctx, user.ID); err != nil || got.Name != "Bob Builder" {
		t.Fatalf("a 201-character name was saved: %d characters (err %v)", len([]rune(got.Name)), err)
	}
	rec = testutil.DoRequest(t, router, "PATCH", "/api/auth/me", map[string]string{"name": strings.Repeat("é", 200)}, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
}

// TestIntegrationUpdateMeRefusedWhileImpersonating: a platform operator logged
// in as an org sees the org owner's name and email in Settings, while the
// session's user is the operator. Saving that form would rename the operator,
// so the profile can't be saved in that mode.
func TestIntegrationUpdateMeRefusedWhileImpersonating(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	home := testutil.SeedTenant(t, q, "OperatorHome")
	operator := testutil.SeedUser(t, q, home.ID, "operator@test.com", "pass123")
	customer := testutil.SeedTenant(t, q, "CustomerCorp")
	testutil.SeedUser(t, q, customer.ID, "boss@customer.test", "pass123")

	token, err := auth.GenerateImpersonationToken(operator.ID, customer.ID, auth.RoleOwner, testutil.TestJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("impersonation token: %v", err)
	}
	rec := testutil.DoRequestWithRawToken(t, router, "PATCH", "/api/auth/me", map[string]string{"name": "Customer Boss"}, token)
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	if got, err := q.GetUser(ctx, operator.ID); err != nil || got.Name != operator.Name {
		t.Fatalf("operator renamed to %q while impersonating (err %v)", got.Name, err)
	}
}
