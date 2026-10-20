package auth_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mtdefaults"
	"openloc/internal/testutil"
)

// TestCreateOrg_FreeEmailBlocked: a personal-email identity can't create an org.
func TestCreateOrg_FreeEmailBlocked(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, nil, &fakeSender{})

	user := testutil.SeedIdentity(t, q, "joe@gmail.com")
	code, body := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), nil)

	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%v)", code, body)
	}
	if c := errCode(body); c != "free_email" {
		t.Errorf("expected code=free_email, got %q", c)
	}
}

// TestCreateOrg_DomainTaken: a domain already owned by a tenant can't be
// re-created — the user must be invited instead.
func TestCreateOrg_DomainTaken(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, nil, &fakeSender{})

	existing := testutil.SeedTenant(t, q, "Example")
	domain := "example.com"
	if err := q.SetTenantDomain(context.Background(), db.SetTenantDomainParams{ID: existing.ID, Domain: &domain}); err != nil {
		t.Fatalf("set domain: %v", err)
	}

	user := testutil.SeedIdentity(t, q, "jane@example.com")
	code, body := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), nil)

	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (%v)", code, body)
	}
	if c := errCode(body); c != "domain_taken" {
		t.Errorf("expected code=domain_taken, got %q", c)
	}
}

// TestCreateOrg_DomainFreeOnceItsOrgIsClosed: an organization being deleted
// gives up its domain at once, so a new organization for that domain can be
// created while the old one's data is still being removed.
func TestCreateOrg_DomainFreeOnceItsOrgIsClosed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, nil, &fakeSender{})

	closing := testutil.SeedTenant(t, q, "Example")
	domain := "example.com"
	if err := q.SetTenantDomain(context.Background(), db.SetTenantDomainParams{ID: closing.ID, Domain: &domain}); err != nil {
		t.Fatalf("set domain: %v", err)
	}
	if _, err := q.MarkTenantDeleting(context.Background(), closing.ID); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}

	user := testutil.SeedIdentity(t, q, "jane@example.com")
	code, body := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for the closed org's domain, got %d (%v)", code, body)
	}
}

// TestCreateOrg_HappyPath: a work-email identity creates a pending org, becomes
// its owner, and operators are emailed.
func TestCreateOrg_HappyPath(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	r := newSignupRouter(pool, []string{"ops@example.com"}, sender)

	user := testutil.SeedIdentity(t, q, "owner@acmecorp.com")
	code, body := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), map[string]any{})

	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%v)", code, body)
	}
	if body["status"] != "pending" {
		t.Errorf("expected status=pending, got %v", body["status"])
	}

	// Tenant exists, pending, domain-bound.
	domain := "acmecorp.com"
	tenant, err := q.GetTenantByDomain(context.Background(), &domain)
	if err != nil {
		t.Fatalf("tenant not created for domain: %v", err)
	}
	if tenant.Status != "pending" {
		t.Errorf("expected tenant status=pending, got %q", tenant.Status)
	}

	// Caller is the owner.
	m, err := q.GetMembership(context.Background(), db.GetMembershipParams{UserID: user.ID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("owner membership missing: %v", err)
	}
	if m.Role != "owner" {
		t.Errorf("expected role=owner, got %q", m.Role)
	}

	// ...and with the MT engines this deployment supplies, so it can translate
	// before anyone enters a key. Provisioning is
	// wired into org creation and nowhere else — no startup sweep backs it up —
	// so this asserts the real path rather than mtdefaults in isolation.
	engines, err := q.ListMTEngines(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("list MT engines: %v", err)
	}
	got := map[string]db.MtEngine{}
	for _, e := range engines {
		got[e.EngineType] = e
	}
	// What the org receives is exactly what the deployment supplies — asked,
	// not assumed. Hardcoding "mock only" passes locally and fails in the gate,
	// where LIBRETRANSLATE_URL is set: the invariant is the MATCH, not the list.
	want := mtdefaults.Available()
	if len(want) == 0 {
		t.Fatal("this deployment supplies no engines, so the test proves nothing")
	}
	if len(got) != len(want) {
		t.Errorf("a new org got %d engine(s), want the %d this deployment supplies: %+v", len(got), len(want), engines)
	}
	for _, w := range want {
		if _, ok := got[w.EngineType]; !ok {
			t.Errorf("a new org got no %s engine; has %+v", w.EngineType, engines)
		}
	}
	for _, e := range engines {
		if !e.DeploymentManaged {
			t.Errorf("%s is not marked as provisioned with the org", e.EngineType)
		}
		if !e.Enabled {
			t.Errorf("%s arrives disabled — nothing would translate through it", e.EngineType)
		}
	}

	// Operator was notified.
	to := sender.toList()
	if len(to) != 1 || to[0] != "ops@example.com" {
		t.Errorf("expected one operator email to ops@example.com, got %v", to)
	}
}

// TestPendingGate: a pending org's owner is blocked from app routes with
// org_pending, and approval unblocks WITHOUT a re-login (same token).
func TestPendingGate(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, nil, &fakeSender{})

	// Create a pending org owned by the user.
	user := testutil.SeedIdentity(t, q, "owner@acmecorp.com")
	code, _ := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(user.ID), map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("signup failed: %d", code)
	}
	domain := "acmecorp.com"
	tenant, err := q.GetTenantByDomain(context.Background(), &domain)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}

	// A tenant token for the pending org is blocked on app routes.
	tenantToken := testutil.TestJWTWithRole(user.ID, tenant.ID, "owner")
	code, body := do(t, r, "GET", "/api/members", tenantToken, nil)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 while pending, got %d", code)
	}
	if c := errCode(body); c != "org_pending" {
		t.Errorf("expected code=org_pending, got %q", c)
	}

	// Identity routes still work while pending (/auth/me).
	code, _ = do(t, r, "GET", "/api/auth/me", tenantToken, nil)
	if code != http.StatusOK {
		t.Errorf("expected /auth/me 200 while pending, got %d", code)
	}

	// Approve — same token now passes (no re-login).
	if _, err := q.SetTenantStatus(context.Background(), db.SetTenantStatusParams{ID: tenant.ID, Status: "approved"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	code, _ = do(t, r, "GET", "/api/members", tenantToken, nil)
	if code != http.StatusOK {
		t.Errorf("expected 200 after approval, got %d", code)
	}
}

// TestPlatformApprovalSurface: only allowlisted operators can list/approve;
// approval flips status and emails the owner.
func TestPlatformApprovalSurface(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	sender := &fakeSender{}
	r := newSignupRouter(pool, []string{"ops@example.com"}, sender)

	// A pending signup.
	owner := testutil.SeedIdentity(t, q, "owner@acmecorp.com")
	if code, _ := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(owner.ID), map[string]any{}); code != http.StatusCreated {
		t.Fatalf("signup failed: %d", code)
	}
	domain := "acmecorp.com"
	tenant, _ := q.GetTenantByDomain(context.Background(), &domain)

	statusURL := "/api/platform/orgs/" + strconv.FormatInt(tenant.ID, 10) + "/status"

	// Non-operator identity is forbidden on both list + status.
	stranger := testutil.SeedIdentity(t, q, "random@somewhere.com")
	if code, _ := do(t, r, "GET", "/api/platform/orgs", testutil.IdentityJWT(stranger.ID), nil); code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-operator list, got %d", code)
	}
	if code, _ := do(t, r, "POST", statusURL, testutil.IdentityJWT(stranger.ID), map[string]any{"status": "approved"}); code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-operator status change, got %d", code)
	}

	// Operator can list every org and sees the pending one with its status.
	operator := testutil.SeedIdentity(t, q, "ops@example.com")
	opToken := testutil.IdentityJWT(operator.ID)
	code, body := do(t, r, "GET", "/api/platform/orgs", opToken, nil)
	if code != http.StatusOK {
		t.Fatalf("operator list failed: %d", code)
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("expected 1 org, got %v", body["data"])
	}
	if row := data[0].(map[string]any); row["status"] != "pending" || row["owner_email"] != "owner@acmecorp.com" {
		t.Errorf("unexpected org row: %v", row)
	}

	// Invalid status is rejected.
	if code, _ := do(t, r, "POST", statusURL, opToken, map[string]any{"status": "bogus"}); code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d", code)
	}

	// Approve flips status + emails the owner.
	sender.mu.Lock()
	sender.sent = nil // drop the signup-notice email so we assert on the approval one
	sender.mu.Unlock()
	if code, _ := do(t, r, "POST", statusURL, opToken, map[string]any{"status": "approved"}); code != http.StatusOK {
		t.Fatalf("approve failed: %d", code)
	}
	if updated, _ := q.GetTenant(context.Background(), tenant.ID); updated.Status != "approved" {
		t.Errorf("expected approved, got %q", updated.Status)
	}
	if to := sender.toList(); len(to) != 1 || to[0] != "owner@acmecorp.com" {
		t.Errorf("expected approval email to owner, got %v", to)
	}

	// Disable (suspend) an approved org: status flips, owner emailed, and the
	// owner's app access is gated again (org_pending) without touching tokens.
	sender.mu.Lock()
	sender.sent = nil
	sender.mu.Unlock()
	if code, _ := do(t, r, "POST", statusURL, opToken, map[string]any{"status": "suspended"}); code != http.StatusOK {
		t.Fatalf("disable failed: %d", code)
	}
	if updated, _ := q.GetTenant(context.Background(), tenant.ID); updated.Status != "suspended" {
		t.Errorf("expected suspended, got %q", updated.Status)
	}
	if to := sender.toList(); len(to) != 1 || to[0] != "owner@acmecorp.com" {
		t.Errorf("expected disable email to owner, got %v", to)
	}
	ownerToken := testutil.TestJWTWithRole(owner.ID, tenant.ID, "owner")
	if code, gbody := do(t, r, "GET", "/api/members", ownerToken, nil); code != http.StatusForbidden || errCode(gbody) != "org_pending" {
		t.Errorf("disabled org should gate app routes, got %d (%v)", code, gbody)
	}

	// Re-enable restores access.
	if code, _ := do(t, r, "POST", statusURL, opToken, map[string]any{"status": "approved"}); code != http.StatusOK {
		t.Fatalf("re-enable failed: %d", code)
	}
	if code, _ := do(t, r, "GET", "/api/members", ownerToken, nil); code != http.StatusOK {
		t.Errorf("re-enabled org should allow app routes, got %d", code)
	}
}

// TestPlatformLoginAsOrg: an operator can enter an approved org they don't
// belong to; non-operators can't; non-approved orgs are refused.
func TestPlatformLoginAsOrg(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, []string{"ops@example.com"}, &fakeSender{})

	// An approved org owned by someone else, and an operator with no membership.
	owner := testutil.SeedIdentity(t, q, "owner@acmecorp.com")
	if code, _ := do(t, r, "POST", "/api/auth/orgs", testutil.IdentityJWT(owner.ID), map[string]any{}); code != http.StatusCreated {
		t.Fatalf("signup failed: %d", code)
	}
	domain := "acmecorp.com"
	tenant, _ := q.GetTenantByDomain(context.Background(), &domain)
	if _, err := q.SetTenantStatus(context.Background(), db.SetTenantStatusParams{ID: tenant.ID, Status: "approved"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	operator := testutil.SeedIdentity(t, q, "ops@example.com")
	opToken := testutil.IdentityJWT(operator.ID)
	loginURL := "/api/platform/orgs/" + strconv.FormatInt(tenant.ID, 10) + "/login"

	// Non-operator can't impersonate.
	stranger := testutil.SeedIdentity(t, q, "random@somewhere.com")
	if code, _ := do(t, r, "POST", loginURL, testutil.IdentityJWT(stranger.ID), nil); code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-operator, got %d", code)
	}

	// Operator enters the org: gets a token scoped to the target with owner
	// access, and can reach app routes there despite having no membership.
	code, body := do(t, r, "POST", loginURL, opToken, nil)
	if code != http.StatusOK {
		t.Fatalf("login-as failed: %d (%v)", code, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatal("expected a token in response")
	}
	// The minted token is scoped to the target org (/me wraps the profile in "data").
	mcode, meResp := do(t, r, "GET", "/api/auth/me", token, nil)
	me, _ := meResp["data"].(map[string]any)
	if mcode != http.StatusOK || me == nil || int64(me["tenant_id"].(float64)) != tenant.ID || me["role"] != "owner" {
		t.Errorf("expected token scoped to org %d as owner, got %v", tenant.ID, meResp)
	}
	// The session presents AS the org: /me shows the org's owner as the identity
	// and drops the platform flag, so it reads like that org is logged in.
	if me["email"] != "owner@acmecorp.com" || me["is_platform_admin"] != false {
		t.Errorf("expected /me to present as org owner (email/is_platform_admin), got %v", me)
	}
	// And it works on a real app route (operator is not a member — proves the
	// token, not a membership, grants access).
	if acode, _ := do(t, r, "GET", "/api/members", token, nil); acode != http.StatusOK {
		t.Errorf("operator token should reach app routes, got %d", acode)
	}
	// /me flags the impersonation.
	if _, meResp := do(t, r, "GET", "/api/auth/me", token, nil); meResp["data"].(map[string]any)["impersonating"] != true {
		t.Errorf("expected /me impersonating=true, got %v", meResp["data"])
	}
	// The impersonation token must NOT reach the platform surface — you can't
	// manage the platform while acting as a tenant (even though the operator's
	// email is still allowlisted).
	if pcode, _ := do(t, r, "GET", "/api/platform/orgs", token, nil); pcode != http.StatusForbidden {
		t.Errorf("impersonation token must be refused on platform routes, got %d", pcode)
	}

	// A non-approved org is refused (it'd be gated anyway).
	if _, err := q.SetTenantStatus(context.Background(), db.SetTenantStatusParams{ID: tenant.ID, Status: "suspended"}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if code, gbody := do(t, r, "POST", loginURL, opToken, nil); code != http.StatusConflict || errCode(gbody) != "org_not_active" {
		t.Errorf("expected 409 org_not_active for suspended org, got %d (%v)", code, gbody)
	}
}

// TestPlatformOwnOrgRestrictions: an operator can't disable or "log in as" an
// org they belong to, and the list flags those rows is_self.
func TestPlatformOwnOrgRestrictions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	r := newSignupRouter(pool, []string{"ops@example.com"}, &fakeSender{})

	// The operator also owns an org (seeded tenants default to status=approved).
	operator := testutil.SeedIdentity(t, q, "ops@example.com")
	own := testutil.SeedTenant(t, q, "Ops Org")
	testutil.SeedMembership(t, q, operator.ID, own.ID, "owner")

	// A different approved org the operator does NOT belong to.
	other := testutil.SeedTenant(t, q, "Other Org")
	otherOwner := testutil.SeedIdentity(t, q, "owner@other.com")
	testutil.SeedMembership(t, q, otherOwner.ID, other.ID, "owner")

	opToken := testutil.IdentityJWT(operator.ID)
	ownStatus := "/api/platform/orgs/" + strconv.FormatInt(own.ID, 10) + "/status"
	ownLogin := "/api/platform/orgs/" + strconv.FormatInt(own.ID, 10) + "/login"

	// Can't log in as your own org.
	if code, gbody := do(t, r, "POST", ownLogin, opToken, nil); code != http.StatusConflict || errCode(gbody) != "own_org" {
		t.Errorf("login-as own org should be 409 own_org, got %d (%v)", code, gbody)
	}
	// Can't disable your own org.
	if code, gbody := do(t, r, "POST", ownStatus, opToken, map[string]any{"status": "suspended"}); code != http.StatusConflict || errCode(gbody) != "own_org" {
		t.Errorf("disable own org should be 409 own_org, got %d (%v)", code, gbody)
	}
	// But disabling an org you DON'T belong to still works.
	otherStatus := "/api/platform/orgs/" + strconv.FormatInt(other.ID, 10) + "/status"
	if code, _ := do(t, r, "POST", otherStatus, opToken, map[string]any{"status": "suspended"}); code != http.StatusOK {
		t.Errorf("disable other org should succeed, got %d", code)
	}

	// The list flags is_self correctly per row.
	_, body := do(t, r, "GET", "/api/platform/orgs", opToken, nil)
	data, _ := body["data"].([]any)
	sawOwn, sawOther := false, false
	for _, row := range data {
		m := row.(map[string]any)
		switch int64(m["tenant_id"].(float64)) {
		case own.ID:
			sawOwn = true
			if m["is_self"] != true {
				t.Errorf("own org should have is_self=true, got %v", m["is_self"])
			}
		case other.ID:
			sawOther = true
			if m["is_self"] != false {
				t.Errorf("other org should have is_self=false, got %v", m["is_self"])
			}
		}
	}
	if !sawOwn || !sawOther {
		t.Errorf("expected both orgs in list (own=%v other=%v)", sawOwn, sawOther)
	}
}
