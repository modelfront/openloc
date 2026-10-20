package auth_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Password auth integration tests. Router helpers (`do`, fakeSender,
// newSignupRouter, errCode) are shared with signup_test.go.

// seedInvite inserts an invite row directly (the tenant-facing endpoint can't
// create owner invites, and tests need control over expiry).
func seedInvite(t *testing.T, q db.Querier, tenantID, invitedBy int64, email, role string, expiresIn time.Duration) db.Invite {
	t.Helper()
	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	inv, err := q.CreateInvite(context.Background(), db.CreateInviteParams{
		TenantID:  tenantID,
		Email:     email,
		Role:      role,
		InvitedBy: invitedBy,
		Token:     token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
	})
	if err != nil {
		t.Fatalf("seedInvite: %v", err)
	}
	return inv
}

func TestPasswordLogin_SingleOrg(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "PwLogin Corp")
	user := testutil.SeedUser(t, q, tenant.ID, "pw@test.com", "hunter2boat")

	status, body := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "PW@test.com", "password": "hunter2boat", // mixed case must normalize
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", status, body)
	}
	if body["needs_org_selection"] != false {
		t.Fatalf("expected direct landing for single-org user, got %v", body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatal("expected a session token")
	}
	// The tenant token must actually work against an org-scoped route.
	status, meBody := do(t, router, "GET", "/api/auth/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("token from password login rejected: %d (%v)", status, meBody)
	}
	userInfo, _ := body["user"].(map[string]any)
	if userInfo["email"] != user.Email {
		t.Fatalf("expected user %s in response, got %v", user.Email, userInfo)
	}
}

func TestPasswordLogin_MultiOrgNeedsPicker(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenantA := testutil.SeedTenant(t, q, "PwMultiA")
	tenantB := testutil.SeedTenant(t, q, "PwMultiB")
	user := testutil.SeedUser(t, q, tenantA.ID, "multi@test.com", "hunter2boat")
	testutil.SeedMembership(t, q, user.ID, tenantB.ID, "admin")

	status, body := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "multi@test.com", "password": "hunter2boat",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", status, body)
	}
	if body["needs_org_selection"] != true {
		t.Fatalf("expected org picker for multi-org user, got %v", body)
	}
	orgs, _ := body["orgs"].([]any)
	if len(orgs) != 2 {
		t.Fatalf("expected 2 orgs in picker payload, got %d", len(orgs))
	}
}

// TestPasswordLogin_UniformFailures locks in the anti-enumeration contract:
// wrong password, unknown email, OAuth-only account, and deactivated user must
// be byte-for-byte indistinguishable to the client.
func TestPasswordLogin_UniformFailures(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "PwFail Corp")
	testutil.SeedUser(t, q, tenant.ID, "real@test.com", "hunter2boat")
	oauthOnly := testutil.SeedIdentity(t, q, "oauth-only@test.com") // nil password_hash
	deactivated := testutil.SeedUser(t, q, tenant.ID, "gone@test.com", "hunter2boat")
	if err := q.DeactivateUser(context.Background(), deactivated.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	_ = oauthOnly

	cases := map[string]map[string]string{
		"wrong password": {"email": "real@test.com", "password": "not-the-password"},
		"unknown email":  {"email": "nobody@test.com", "password": "hunter2boat"},
		"oauth-only":     {"email": "oauth-only@test.com", "password": "hunter2boat"},
		"deactivated":    {"email": "gone@test.com", "password": "hunter2boat"},
	}
	var firstBody map[string]any
	for name, req := range cases {
		status, body := do(t, router, "POST", "/api/auth/login", "", req)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d (%v)", name, status, body)
		}
		if firstBody == nil {
			firstBody = body
			continue
		}
		if len(body) != len(firstBody) || body["error"] == nil {
			t.Fatalf("%s: failure body differs — enumeration leak: %v vs %v", name, body, firstBody)
		}
		errA, _ := body["error"].(map[string]any)
		errB, _ := firstBody["error"].(map[string]any)
		if errA["message"] != errB["message"] {
			t.Fatalf("%s: failure message differs — enumeration leak: %v vs %v", name, errA, errB)
		}
	}
}

// TestPasswordLogin_MaterializesPendingInvite verifies password login runs the
// same membership materialization as OAuth: a pending email-matched invite
// becomes a membership at login.
func TestPasswordLogin_MaterializesPendingInvite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenantA := testutil.SeedTenant(t, q, "PwInvA")
	tenantB := testutil.SeedTenant(t, q, "PwInvB")
	user := testutil.SeedUser(t, q, tenantA.ID, "invited@test.com", "hunter2boat")
	inviter := testutil.SeedUser(t, q, tenantB.ID, "inviter@test.com", "pass")
	seedInvite(t, q, tenantB.ID, inviter.ID, "invited@test.com", "linguist", 7*24*time.Hour)

	status, body := do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "invited@test.com", "password": "hunter2boat",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", status, body)
	}
	m, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: user.ID, TenantID: tenantB.ID})
	if err != nil {
		t.Fatalf("expected invite to materialize into a membership: %v", err)
	}
	if m.Role != "linguist" {
		t.Fatalf("expected linguist role from invite, got %q", m.Role)
	}
}

func TestAcceptInvite_NewUserFullFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Accept Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, inviter.ID, "fresh@test.com", "project_manager", 7*24*time.Hour)

	status, body := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "brand-new-pw-1", "name": "Fresh Person",
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", status, body)
	}
	if body["needs_org_selection"] != false {
		t.Fatalf("single-org accept should land directly, got %v", body)
	}

	// Account exists with the INVITE's email and the given name.
	user, err := q.GetUserByEmail(context.Background(), "fresh@test.com")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.Name != "Fresh Person" {
		t.Fatalf("expected name from request, got %q", user.Name)
	}
	// Membership carries the invite's role; the invite is consumed.
	m, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil || m.Role != "project_manager" {
		t.Fatalf("expected project_manager membership, got %v / %v", m, err)
	}
	got, err := q.GetInviteByToken(context.Background(), inv.Token)
	if err != nil || !got.AcceptedAt.Valid {
		t.Fatalf("expected invite marked accepted, got %v / %v", got, err)
	}
	// And the password actually logs in.
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "fresh@test.com", "password": "brand-new-pw-1",
	})
	if status != http.StatusOK {
		t.Fatalf("password set at accept should log in, got %d", status)
	}
}

// TestAcceptInvite_TokenOracle: unknown, expired, and already-accepted tokens
// are indistinguishable flat 404s.
func TestAcceptInvite_TokenOracle(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Oracle Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")

	expired := seedInvite(t, q, tenant.ID, inviter.ID, "late@test.com", "linguist", -time.Hour)
	accepted := seedInvite(t, q, tenant.ID, inviter.ID, "done@test.com", "linguist", 7*24*time.Hour)
	if _, err := q.AcceptInvite(context.Background(), accepted.ID); err != nil {
		t.Fatalf("pre-accept: %v", err)
	}

	for name, token := range map[string]string{
		"unknown": "definitely-not-a-token",
		"expired": expired.Token,
		"used":    accepted.Token,
	} {
		status, _ := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
			"token": token, "password": "whatever-pw-1",
		})
		if status != http.StatusNotFound {
			t.Fatalf("%s token: expected flat 404, got %d", name, status)
		}
	}
}

// TestAcceptInvite_ExistingUserUntouched is a security boundary:
// an invite token must NEVER set or change credentials on an existing account —
// a forwarded or leaked invite link would then take that account over.
func TestAcceptInvite_ExistingUserUntouched(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenantA := testutil.SeedTenant(t, q, "Victim Corp")
	tenantB := testutil.SeedTenant(t, q, "Attacker Corp")
	victim := testutil.SeedUser(t, q, tenantA.ID, "victim@test.com", "victims-own-pw")
	attacker := testutil.SeedUser(t, q, tenantB.ID, "admin@attacker.com", "pass")
	// Attacker's org invites the victim's email and knows the token.
	inv := seedInvite(t, q, tenantB.ID, attacker.ID, "victim@test.com", "linguist", 7*24*time.Hour)

	status, body := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "attacker-chosen-pw",
	})
	if status != http.StatusConflict || errCode(body) != "account_exists" {
		t.Fatalf("expected 409 account_exists, got %d (%v)", status, body)
	}
	// The failed accept itself must not create a membership. (The victim's own
	// NEXT login legitimately materializes the email-matched invite — an
	// admin-created invite for an email is an authorization grant; only the
	// CREDENTIAL surface is the attack vector here.)
	if _, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: victim.ID, TenantID: tenantB.ID}); err == nil {
		t.Fatal("no membership may be created by a failed accept")
	}
	// The victim's credentials are untouched: old password works, new doesn't.
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "victim@test.com", "password": "victims-own-pw",
	})
	if status != http.StatusOK {
		t.Fatalf("victim's original password must still work, got %d", status)
	}
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "victim@test.com", "password": "attacker-chosen-pw",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("attacker-chosen password must NOT work, got %d", status)
	}
}

func TestAcceptInvite_PasswordPolicy(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Policy Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, inviter.ID, "weak@test.com", "linguist", 7*24*time.Hour)

	for name, pw := range map[string]string{
		"too short": "seven77",
		"too long":  strings.Repeat("x", 73),
	} {
		status, _ := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
			"token": inv.Token, "password": pw,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", name, status)
		}
	}
	// Rejections must not consume the invite or create the account.
	if _, err := q.GetUserByEmail(context.Background(), "weak@test.com"); err == nil {
		t.Fatal("no user may be created on policy rejection")
	}
}

// A name past 200 characters is refused, as in Settings, and creates no
// account.
func TestAcceptInvite_NameTooLong(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Name Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, inviter.ID, "longname@test.com", "linguist", 7*24*time.Hour)

	status, _ := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "long-enough-password", "name": strings.Repeat("é", 201),
	})
	if status != http.StatusBadRequest {
		t.Fatalf("201-character name: expected 400, got %d", status)
	}
	if _, err := q.GetUserByEmail(context.Background(), "longname@test.com"); err == nil {
		t.Fatal("an account was created with a 201-character name")
	}
}

// resetLinkRe pulls the raw token out of the reset email body.
var resetLinkRe = regexp.MustCompile(`/reset-password/([A-Za-z0-9_-]+)`)

func TestPasswordReset_FullFlow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	router := newSignupRouter(pool, nil, sender)
	tenant := testutil.SeedTenant(t, q, "Reset Corp")
	user := testutil.SeedUser(t, q, tenant.ID, "resetme@test.com", "old-password-1")

	// Unknown email: same 200, no email sent.
	status, _ := do(t, router, "POST", "/api/auth/forgot-password", "", map[string]string{"email": "ghost@test.com"})
	if status != http.StatusOK {
		t.Fatalf("forgot for unknown email must 200, got %d", status)
	}
	if len(sender.toList()) != 0 {
		t.Fatalf("no email may be sent for unknown accounts, got %v", sender.toList())
	}

	// Known email: 200 + reset link delivered.
	status, _ = do(t, router, "POST", "/api/auth/forgot-password", "", map[string]string{"email": "resetme@test.com"})
	if status != http.StatusOK {
		t.Fatalf("forgot: expected 200, got %d", status)
	}
	sender.mu.Lock()
	if len(sender.sent) != 1 {
		sender.mu.Unlock()
		t.Fatalf("expected 1 reset email, got %d", len(sender.sent))
	}
	match := resetLinkRe.FindStringSubmatch(sender.sent[0].Body)
	sender.mu.Unlock()
	if match == nil {
		t.Fatal("reset email must contain a /reset-password/{token} link")
	}
	rawToken := match[1]

	// Redeem: new password set, auto-logged-in.
	status, body := do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{
		"token": rawToken, "password": "new-password-9",
	})
	if status != http.StatusOK {
		t.Fatalf("reset: expected 200, got %d (%v)", status, body)
	}
	if tok, _ := body["token"].(string); tok == "" {
		t.Fatal("reset must auto-login (token in response)")
	}
	// Old password dead, new one works.
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{"email": "resetme@test.com", "password": "old-password-1"})
	if status != http.StatusUnauthorized {
		t.Fatalf("old password must be rejected after reset, got %d", status)
	}
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{"email": "resetme@test.com", "password": "new-password-9"})
	if status != http.StatusOK {
		t.Fatalf("new password must log in, got %d", status)
	}
	// Single-use: replaying the token fails.
	status, body = do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{
		"token": rawToken, "password": "another-pw-33",
	})
	if status != http.StatusBadRequest || errCode(body) != "invalid_token" {
		t.Fatalf("token replay must 400 invalid_token, got %d (%v)", status, body)
	}
	_ = user
}

// TestPasswordReset_OAuthOnlyAccountGainsPassword: forgot-password is the
// sanctioned way to ADD a password to an OAuth-created account.
func TestPasswordReset_OAuthOnlyAccountGainsPassword(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	router := newSignupRouter(pool, nil, sender)
	tenant := testutil.SeedTenant(t, q, "OAuthReset Corp")
	user := testutil.SeedIdentity(t, q, "google-person@test.com") // no password
	testutil.SeedMembership(t, q, user.ID, tenant.ID, "admin")

	status, _ := do(t, router, "POST", "/api/auth/forgot-password", "", map[string]string{"email": "google-person@test.com"})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	sender.mu.Lock()
	if len(sender.sent) != 1 {
		sender.mu.Unlock()
		t.Fatal("OAuth-only accounts must be able to request a password")
	}
	rawToken := resetLinkRe.FindStringSubmatch(sender.sent[0].Body)[1]
	sender.mu.Unlock()

	status, _ = do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{
		"token": rawToken, "password": "my-first-pw-1",
	})
	if status != http.StatusOK {
		t.Fatalf("reset on OAuth-only account: expected 200, got %d", status)
	}
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "google-person@test.com", "password": "my-first-pw-1",
	})
	if status != http.StatusOK {
		t.Fatalf("OAuth-only account must password-login after reset, got %d", status)
	}
}

// TestPasswordReset_RerequestInvalidatesPrior: requesting a new link kills the
// old one.
func TestPasswordReset_RerequestInvalidatesPrior(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	router := newSignupRouter(pool, nil, sender)
	tenant := testutil.SeedTenant(t, q, "Rotate Corp")
	testutil.SeedUser(t, q, tenant.ID, "rotate@test.com", "old-password-1")

	for range 2 {
		status, _ := do(t, router, "POST", "/api/auth/forgot-password", "", map[string]string{"email": "rotate@test.com"})
		if status != http.StatusOK {
			t.Fatalf("forgot: expected 200, got %d", status)
		}
	}
	sender.mu.Lock()
	if len(sender.sent) != 2 {
		sender.mu.Unlock()
		t.Fatalf("expected 2 emails, got %d", len(sender.sent))
	}
	first := resetLinkRe.FindStringSubmatch(sender.sent[0].Body)[1]
	second := resetLinkRe.FindStringSubmatch(sender.sent[1].Body)[1]
	sender.mu.Unlock()

	status, _ := do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{"token": first, "password": "should-not-work-1"})
	if status != http.StatusBadRequest {
		t.Fatalf("first (rotated-out) token must fail, got %d", status)
	}
	status, _ = do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{"token": second, "password": "should-work-123"})
	if status != http.StatusOK {
		t.Fatalf("second (current) token must work, got %d", status)
	}
}

// TestPasswordReset_ExpiredToken: an expired token is a flat 400.
func TestPasswordReset_ExpiredToken(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Expired Corp")
	user := testutil.SeedUser(t, q, tenant.ID, "expired@test.com", "old-password-1")

	raw, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := q.CreatePasswordResetToken(context.Background(), db.CreatePasswordResetTokenParams{
		UserID:    user.ID,
		TokenHash: auth.HashPAT(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}
	status, _ := do(t, router, "POST", "/api/auth/reset-password", "", map[string]string{"token": raw, "password": "new-password-9"})
	if status != http.StatusBadRequest {
		t.Fatalf("expired token must 400, got %d", status)
	}
}

// TestMaterialize_TokenInviteEmailMismatch: the explicit-token branch must not
// attach an org when the invited email differs from the signed-in identity
// — but the same invite still materializes for the RIGHT email.
func TestMaterialize_TokenInviteEmailMismatch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	h := auth.NewHandler(q, pool, testutil.TestJWTSecret, "http://localhost:5173", true, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Mismatch Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, inviter.ID, "right@test.com", "linguist", 7*24*time.Hour)

	wrong := testutil.SeedIdentity(t, q, "wrong@test.com")
	h.MaterializeMembershipsForTest(context.Background(), wrong.ID, "wrong@test.com", inv.Token)
	if _, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: wrong.ID, TenantID: tenant.ID}); err == nil {
		t.Fatal("token invite for a different email must not create a membership")
	}

	right := testutil.SeedIdentity(t, q, "right@test.com")
	h.MaterializeMembershipsForTest(context.Background(), right.ID, "right@test.com", inv.Token)
	if _, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: right.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("token invite for the matching email must materialize: %v", err)
	}
}

// TestMaterialize_ExpiredTokenInviteStillMatchesByEmail pins that
// the token link honors expiry, but the email-matched pass is deliberately
// durable — an expired invite still materializes when the invited email itself
// signs in.
func TestMaterialize_ExpiredTokenInviteStillMatchesByEmail(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	h := auth.NewHandler(q, pool, testutil.TestJWTSecret, "http://localhost:5173", true, nil, nil)
	tenant := testutil.SeedTenant(t, q, "Durable Corp")
	inviter := testutil.SeedUser(t, q, tenant.ID, "boss@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, inviter.ID, "durable@test.com", "linguist", -time.Hour)

	user := testutil.SeedIdentity(t, q, "durable@test.com")
	// Explicit expired token alone: the token branch skips it, but the
	// email-matched pass picks the same invite up — membership exists either way.
	h.MaterializeMembershipsForTest(context.Background(), user.ID, "durable@test.com", inv.Token)
	if _, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID}); err != nil {
		t.Fatalf("email-matched invite must stay durable past expiry (documented D7): %v", err)
	}
}
