package invite_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/testutil"
)

// seedInvite stores an invite directly, expiring in an hour. Tests use it for
// roles the API can't create: owner invites come only from platform operators.
func seedInvite(t *testing.T, q db.Querier, tenantID, invitedBy int64, email, role string) db.Invite {
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
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	return inv
}

func inviteExists(t *testing.T, q db.Querier, tenantID, id int64) bool {
	t.Helper()
	_, err := q.GetInviteTenant(context.Background(), db.GetInviteTenantParams{ID: id, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("get invite: %v", err)
	}
	return true
}

// TestIntegrationInviteManagerScope pins who sees and revokes which invites:
// nobody handles an invite for a role above their own, and a project manager
// handles only linguist invites.
func TestIntegrationInviteManagerScope(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "ScopeCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")

	ownerInv := seedInvite(t, q, tenant.ID, owner.ID, "new-owner@test.com", "owner")
	adminInv := seedInvite(t, q, tenant.ID, owner.ID, "new-admin@test.com", "admin")
	pmInv := seedInvite(t, q, tenant.ID, owner.ID, "new-pm@test.com", "project_manager")
	lingInv := seedInvite(t, q, tenant.ID, owner.ID, "new-ling@test.com", "linguist")

	listEmails := func(userID int64, role string) []string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "GET", "/api/invites", nil, userID, tenant.ID, role)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				Email string `json:"email"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		emails := make([]string, 0, len(resp.Data))
		for _, inv := range resp.Data {
			emails = append(emails, inv.Email)
		}
		slices.Sort(emails)
		return emails
	}
	for _, tc := range []struct {
		name   string
		userID int64
		role   string
		want   []string
	}{
		{"owner sees every invite", owner.ID, "owner", []string{"new-admin@test.com", "new-ling@test.com", "new-owner@test.com", "new-pm@test.com"}},
		{"admin does not see the owner invite", admin.ID, "admin", []string{"new-admin@test.com", "new-ling@test.com", "new-pm@test.com"}},
		{"project manager sees only linguist invites", pm.ID, "project_manager", []string{"new-ling@test.com"}},
	} {
		if got := listEmails(tc.userID, tc.role); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	revoke := func(id, userID int64, role string, want int) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/invites/%d", id), nil, userID, tenant.ID, role)
		testutil.AssertStatus(t, rec, want)
	}
	revoke(adminInv.ID, pm.ID, "project_manager", http.StatusNotFound)
	if !inviteExists(t, q, tenant.ID, adminInv.ID) {
		t.Fatal("a project manager revoked an admin invite")
	}
	revoke(ownerInv.ID, admin.ID, "admin", http.StatusNotFound)
	if !inviteExists(t, q, tenant.ID, ownerInv.ID) {
		t.Fatal("an admin revoked an owner invite")
	}
	revoke(999999, owner.ID, "owner", http.StatusNotFound)

	// Linguists are stopped by the route gate.
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/invites", nil, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
	revoke(lingInv.ID, linguist.ID, "linguist", http.StatusForbidden)

	revoke(lingInv.ID, pm.ID, "project_manager", http.StatusNoContent)
	revoke(pmInv.ID, admin.ID, "admin", http.StatusNoContent)
	revoke(ownerInv.ID, owner.ID, "owner", http.StatusNoContent)
	for _, inv := range []db.Invite{lingInv, pmInv, ownerInv} {
		if inviteExists(t, q, tenant.ID, inv.ID) {
			t.Errorf("invite %s is still there after its revoke", inv.Email)
		}
	}
}

// fakeSender records the emails it's asked to send (satisfies notif.Sender).
// With fail set it refuses every email; onSend runs while an email is "out",
// so a test can change the invite meanwhile.
type fakeSender struct {
	calls       int
	lastTo      string
	lastSubject string
	lastBody    string
	fail        error
	onSend      func()
}

func (f *fakeSender) SendEmail(_ context.Context, to, subject string, body emaillayout.Email) error {
	f.calls++
	if f.onSend != nil {
		f.onSend()
	}
	if f.fail != nil {
		return f.fail
	}
	f.lastTo = to
	f.lastSubject = subject
	f.lastBody = body.HTML()
	return nil
}

func getInvite(t *testing.T, q db.Querier, tenantID, id int64) db.Invite {
	t.Helper()
	inv, err := q.GetInviteTenant(context.Background(), db.GetInviteTenantParams{ID: id, TenantID: tenantID})
	if err != nil {
		t.Fatalf("get invite: %v", err)
	}
	return inv
}

// TestIntegrationInviteLinguistRole verifies a linguist invite is created, and
// that owner and unknown roles are refused.
func TestIntegrationInviteLinguistRole(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "VendorInviteCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "v@test.com", "role": "linguist",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var resp struct {
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if resp.Data.Role != "linguist" {
		t.Fatalf("expected invite role linguist, got %q", resp.Data.Role)
	}
	// Acceptance (invite → linguist membership) is covered by the auth package's
	// TestInviteMaterialize_* tests (accept happens during Google sign-in).

	// An unknown role is rejected, and so is owner: ownership comes from a role
	// change by an owner or from a platform operator, never from a tenant invite.
	for _, role := range []string{"superadmin", "owner"} {
		rec = testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
			"email": "x@test.com", "role": role,
		}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusBadRequest)
	}
}

// TestIntegrationInviteSendsEmail verifies the invite handler emails the
// /invite/{token} link via the configured sender.
func TestIntegrationInviteSendsEmail(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	fake := &fakeSender{}
	router := api.NewRouter(api.Deps{
		Queries:     db.New(pool),
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: fake,
	})
	tenant := testutil.SeedTenant(t, q, "EmailCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "v@test.com", "role": "linguist",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	if fake.calls != 1 {
		t.Fatalf("expected exactly 1 invite email, got %d", fake.calls)
	}
	if fake.lastTo != "v@test.com" {
		t.Fatalf("expected email to v@test.com, got %q", fake.lastTo)
	}
	if !strings.Contains(fake.lastBody, "/invite/") {
		t.Fatalf("expected invite link in email body, got %q", fake.lastBody)
	}
	if !strings.Contains(fake.lastBody, emaillayout.HeaderMarker) {
		t.Fatalf("invite email does not use the shared layout: %q", fake.lastBody)
	}
}

func TestIntegrationInviteCRUD(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "linguist")

	// Owner creates invite — should succeed
	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "new@test.com",
		"role":  "linguist",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	var createResp struct {
		Data struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &createResp)
	if createResp.Data.Email != "new@test.com" {
		t.Errorf("expected email new@test.com, got %q", createResp.Data.Email)
	}

	inviteID := createResp.Data.ID

	// Member creates invite — should get 403
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "another@test.com",
	}, member.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)

	// Owner lists invites
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/invites", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	// Duplicate invite — should conflict
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "new@test.com",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	// Acceptance (invite → membership on Google sign-in) is covered by the auth
	// package's TestInviteMaterialize_* tests.

	// Delete invite (revoke a pending invite).
	rec = testutil.DoRequestWithRole(t, router, "DELETE", fmt.Sprintf("/api/invites/%d", inviteID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
}

// TestIntegrationInviteResponsesCarryNoToken: the invite token proves that its
// holder reads the invited mailbox, so no API response may carry it.
func TestIntegrationInviteResponsesCarryNoToken(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NoTokenCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	created := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{
		"email": "new-admin@test.com", "role": "admin",
	}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, created, http.StatusCreated)
	stored, err := q.GetPendingInviteByEmailAndTenant(context.Background(), db.GetPendingInviteByEmailAndTenantParams{
		Email: "new-admin@test.com", TenantID: tenant.ID,
	})
	if err != nil {
		t.Fatalf("stored invite: %v", err)
	}
	listed := testutil.DoRequestWithRole(t, router, "GET", "/api/invites", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, listed, http.StatusOK)

	for name, body := range map[string]string{"create": created.Body.String(), "list": listed.Body.String()} {
		if strings.Contains(body, stored.Token) || strings.Contains(body, `"token"`) {
			t.Errorf("%s response carries the invite token: %s", name, body)
		}
	}
}

func TestIntegrationMembersEndpoint(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	testutil.SeedUserWithRole(t, q, tenant.ID, "member@test.com", "pass", "linguist")

	// Owner can list members
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/members", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data []struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 members, got %d", len(resp.Data))
	}

	// Member cannot list members (requires owner/admin)
	member := testutil.SeedUserWithRole(t, q, tenant.ID, "member2@test.com", "pass", "linguist")
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/members", nil, member.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusForbidden)
}
