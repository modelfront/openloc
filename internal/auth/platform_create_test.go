package auth_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Platform org provisioning tests. Shares `do`, fakeSender,
// newSignupRouter, errCode with signup_test.go.

var inviteLinkRe = regexp.MustCompile(`/invite/([A-Za-z0-9_-]+)`)

// seedOperator creates a platform-admin identity (in its own org) and returns
// the user + an identity-scoped bearer token, which is what the /api/platform
// surface runs on.
func seedOperator(t *testing.T, q db.Querier) (db.User, string) {
	t.Helper()
	opTenant := testutil.SeedTenant(t, q, "Platform Ops")
	op := testutil.SeedUser(t, q, opTenant.ID, "op@example.com", "pass")
	return op, testutil.IdentityJWT(op.ID)
}

func TestPlatformCreateOrg_FullChain(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	router := newSignupRouter(pool, []string{"op@example.com"}, sender)
	op, opToken := seedOperator(t, q)

	status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Acme", "owner_email": "Owner@Acme.example.com", "domain": "acme.example.com",
	})
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", status, body)
	}
	tenantInfo, _ := body["tenant"].(map[string]any)
	if tenantInfo["status"] != "approved" {
		t.Fatalf("platform-created org must start approved, got %v", tenantInfo)
	}
	tenantID := int64(tenantInfo["id"].(float64))

	// Owner invite row: owner role, normalized email, operator as inviter.
	inv, err := q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
		Email: "owner@acme.example.com", TenantID: tenantID,
	})
	if err != nil {
		t.Fatalf("owner invite row missing: %v", err)
	}
	if inv.Role != "owner" || inv.InvitedBy != op.ID {
		t.Fatalf("expected owner-role invite by operator, got role=%q invited_by=%d", inv.Role, inv.InvitedBy)
	}
	// Invite email went to the owner and carries the accept link; the response
	// carries the same link for copy-paste fallback.
	sender.mu.Lock()
	if len(sender.sent) != 1 || sender.sent[0].To != "owner@acme.example.com" {
		sender.mu.Unlock()
		t.Fatalf("expected 1 owner invite email, got %+v", sender.sent)
	}
	emailToken := inviteLinkRe.FindStringSubmatch(sender.sent[0].Body)
	sender.mu.Unlock()
	if emailToken == nil || emailToken[1] != inv.Token {
		t.Fatal("owner email must contain the invite link with the real token")
	}
	inviteInfo, _ := body["invite"].(map[string]any)
	if link, _ := inviteInfo["link"].(string); !inviteLinkRe.MatchString(link) {
		t.Fatalf("response must include the shareable invite link, got %v", inviteInfo)
	}

	// Full chain: the invited owner accepts with a password and lands as owner.
	status, acceptBody := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "owner-pw-12345", "name": "The Owner", // gitleaks:allow
	})
	if status != http.StatusOK {
		t.Fatalf("owner accept failed: %d (%v)", status, acceptBody)
	}
	userInfo, _ := acceptBody["user"].(map[string]any)
	if userInfo["role"] != "owner" || int64(userInfo["tenant_id"].(float64)) != tenantID {
		t.Fatalf("accept must land as owner of the new org, got %v", userInfo)
	}
	// And password login works thereafter.
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "owner@acme.example.com", "password": "owner-pw-12345", // gitleaks:allow
	})
	if status != http.StatusOK {
		t.Fatalf("owner must be able to password-login, got %d", status)
	}
}

func TestPlatformCreateOrg_AuthZ(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	tenant := testutil.SeedTenant(t, q, "Rando Corp")
	rando := testutil.SeedUser(t, q, tenant.ID, "rando@test.com", "pass")

	// A non-allowlisted identity is 403 regardless of org role.
	status, _ := do(t, router, "POST", "/api/platform/orgs", testutil.IdentityJWT(rando.ID), map[string]string{
		"name": "Nope", "owner_email": "x@y.com",
	})
	if status != http.StatusForbidden {
		t.Fatalf("non-operator must 403, got %d", status)
	}
	// No token at all is 401.
	status, _ = do(t, router, "POST", "/api/platform/orgs", "", map[string]string{
		"name": "Nope", "owner_email": "x@y.com",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated must 401, got %d", status)
	}
}

func TestPlatformCreateOrg_Validation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	_, opToken := seedOperator(t, q)

	// Free-email domain rejected.
	status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Gmail Org", "owner_email": "a@b.com", "domain": "gmail.com",
	})
	if status != http.StatusBadRequest || errCode(body) != "free_email" {
		t.Fatalf("expected 400 free_email, got %d (%v)", status, body)
	}
	// Duplicate domain rejected.
	domain := "taken.com"
	if _, err := q.CreatePendingTenant(context.Background(), db.CreatePendingTenantParams{
		Name: "Existing", Slug: "existing-domain-org", Domain: &domain,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	status, body = do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Dup", "owner_email": "a@b.com", "domain": "taken.com",
	})
	if status != http.StatusConflict || errCode(body) != "domain_taken" {
		t.Fatalf("expected 409 domain_taken, got %d (%v)", status, body)
	}
	// Missing fields rejected.
	for name, req := range map[string]map[string]string{
		"no name":     {"owner_email": "a@b.com"},
		"no email":    {"name": "X"},
		"bogus email": {"name": "X", "owner_email": "not-an-email"},
	} {
		status, _ = do(t, router, "POST", "/api/platform/orgs", opToken, req)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", name, status)
		}
	}
}

// TestPlatformCreateOrg_ExistingOwnerEmail: the owner may already have an
// account — the invite is created the same way; the accept ENDPOINT refuses
// (existing accounts sign in instead) and a normal sign-in materializes the
// owner membership.
func TestPlatformCreateOrg_ExistingOwnerEmail(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	_, opToken := seedOperator(t, q)
	homeTenant := testutil.SeedTenant(t, q, "Existing Home")
	existing := testutil.SeedUser(t, q, homeTenant.ID, "veteran@test.com", "veteran-pw-1")

	status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Second Org", "owner_email": "veteran@test.com",
	})
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", status, body)
	}
	tenantInfo, _ := body["tenant"].(map[string]any)
	newTenantID := int64(tenantInfo["id"].(float64))
	inv, err := q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
		Email: "veteran@test.com", TenantID: newTenantID,
	})
	if err != nil {
		t.Fatalf("invite row missing: %v", err)
	}

	// Accept endpoint refuses existing accounts (D2)...
	status, acceptBody := do(t, router, "POST", "/api/auth/accept-invite", "", map[string]string{
		"token": inv.Token, "password": "whatever-pw-1",
	})
	if status != http.StatusConflict || errCode(acceptBody) != "account_exists" {
		t.Fatalf("expected 409 account_exists, got %d (%v)", status, acceptBody)
	}
	// ...but a plain password login materializes the owner membership.
	status, _ = do(t, router, "POST", "/api/auth/login", "", map[string]string{
		"email": "veteran@test.com", "password": "veteran-pw-1",
	})
	if status != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", status)
	}
	m, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: existing.ID, TenantID: newTenantID})
	if err != nil || m.Role != "owner" {
		t.Fatalf("expected owner membership after sign-in, got %v / %v", m, err)
	}
}

// TestSetOrgStatus_OwnerlessOrg: a platform-created org whose invite hasn't
// been accepted has no owner membership — status changes must still work
// rather than 404 on the owner lookup.
func TestSetOrgStatus_OwnerlessOrg(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := newSignupRouter(pool, []string{"op@example.com"}, &fakeSender{})
	_, opToken := seedOperator(t, q)

	status, body := do(t, router, "POST", "/api/platform/orgs", opToken, map[string]string{
		"name": "Ownerless", "owner_email": "never-accepts@test.com",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", status)
	}
	tenantInfo, _ := body["tenant"].(map[string]any)
	tenantID := int64(tenantInfo["id"].(float64))

	status, body = do(t, router, "POST",
		"/api/platform/orgs/"+strconv.FormatInt(tenantID, 10)+"/status", opToken,
		map[string]string{"status": "suspended"})
	if status != http.StatusOK {
		t.Fatalf("suspending an ownerless org must work, got %d (%v)", status, body)
	}
	tenant, err := q.GetTenant(context.Background(), tenantID)
	if err != nil || tenant.Status != "suspended" {
		t.Fatalf("expected suspended, got %v / %v", tenant.Status, err)
	}
}
