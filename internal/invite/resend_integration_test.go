package invite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack/errtracktest"
	"openloc/internal/logging"
	"openloc/internal/testutil"
)

// TestIntegrationInviteResend: a resend emails a fresh link and kills the old
// one. It follows the same reach as listing, sends an owner invite with the
// owner email, and refuses an accepted invite. A failed email changes nothing.
func TestIntegrationInviteResend(t *testing.T) {
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
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "ResendCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@test.com", "pass", "project_manager")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@test.com", "pass", "linguist")

	resend := func(id, userID int64, role string, want int) string {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", id), nil, userID, tenant.ID, role)
		testutil.AssertStatus(t, rec, want)
		return rec.Body.String()
	}
	lookup := func(token string, want int) {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+token, nil, owner.ID, tenant.ID)
		testutil.AssertStatus(t, rec, want)
	}
	current := func(id int64) db.Invite { t.Helper(); return getInvite(t, q, tenant.ID, id) }

	// A resend emails a new link and the old link stops working.
	vendor := seedInvite(t, q, tenant.ID, owner.ID, "vendor@test.com", "linguist")
	body := resend(vendor.ID, owner.ID, "owner", http.StatusOK)
	after := current(vendor.ID)
	if after.Token == vendor.Token {
		t.Fatal("resend kept the old token, so a link seen before the resend still works")
	}
	if !after.ExpiresAt.Time.After(vendor.ExpiresAt.Time) {
		t.Errorf("expiry not extended: %v -> %v", vendor.ExpiresAt.Time, after.ExpiresAt.Time)
	}
	if fake.lastTo != "vendor@test.com" || !strings.Contains(fake.lastBody, "/invite/"+after.Token) {
		t.Errorf("resend email went to %q without the new link", fake.lastTo)
	}
	if strings.Contains(body, after.Token) {
		t.Errorf("resend response carries the token: %s", body)
	}
	lookup(vendor.Token, http.StatusNotFound)
	lookup(after.Token, http.StatusOK)

	// A second resend rotates again: only the newest email's link works.
	resend(vendor.ID, owner.ID, "owner", http.StatusOK)
	latest := current(vendor.ID)
	if latest.Token == after.Token || !strings.Contains(fake.lastBody, "/invite/"+latest.Token) {
		t.Fatal("a second resend did not email a fresh link")
	}
	lookup(after.Token, http.StatusNotFound)

	// The same reach as listing.
	pmTarget := seedInvite(t, q, tenant.ID, owner.ID, "vendor2@test.com", "linguist")
	resend(pmTarget.ID, pm.ID, "project_manager", http.StatusOK)
	adminInv := seedInvite(t, q, tenant.ID, owner.ID, "new-admin@test.com", "admin")
	resend(adminInv.ID, pm.ID, "project_manager", http.StatusNotFound)
	ownerInv := seedInvite(t, q, tenant.ID, owner.ID, "new-owner@test.com", "owner")
	resend(ownerInv.ID, admin.ID, "admin", http.StatusNotFound)
	resend(adminInv.ID, linguist.ID, "linguist", http.StatusForbidden)
	if current(adminInv.ID).Token != adminInv.Token || current(ownerInv.ID).Token != ownerInv.Token {
		t.Fatal("a refused resend changed the token")
	}

	// An owner invite goes out with the owner email.
	resend(ownerInv.ID, owner.ID, "owner", http.StatusOK)
	if fake.lastTo != "new-owner@test.com" || !strings.Contains(fake.lastSubject, "owner of ResendCorp") {
		t.Errorf("owner invite resent as %q to %q", fake.lastSubject, fake.lastTo)
	}

	accepted := seedInvite(t, q, tenant.ID, owner.ID, "joined@test.com", "linguist")
	if _, err := q.AcceptInvite(ctx, accepted.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	resend(accepted.ID, owner.ID, "owner", http.StatusConflict)

	// A failed email keeps the old link working and allows a retry at once.
	bounce := seedInvite(t, q, tenant.ID, owner.ID, "bounce@test.com", "linguist")
	fake.fail = errors.New("mail server down")
	resend(bounce.ID, owner.ID, "owner", http.StatusBadGateway)
	fake.fail = nil
	if current(bounce.ID).Token != bounce.Token {
		t.Fatal("a failed resend changed the token")
	}
	lookup(bounce.Token, http.StatusOK)
	resend(bounce.ID, owner.ID, "owner", http.StatusOK)
}

// TestIntegrationInviteResendNeedsEmail: with no email sender configured, a
// resend fails instead of reporting success.
func TestIntegrationInviteResendNeedsEmail(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "NoMailCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, owner.ID, "vendor@test.com", "linguist")

	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", inv.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusServiceUnavailable)
}

// TestIntegrationInviteResendLosesTheRace: Resend stores its new token only if
// the invite is still pending and still holds the token it read. When the
// invite is accepted, or resent by someone else, while the email is out, this
// resend answers 409 and changes nothing.
func TestIntegrationInviteResendLosesTheRace(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	fake := &fakeSender{}
	router := api.NewRouter(api.Deps{
		Queries:     db.New(pool),
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: fake,
	})
	tenant := testutil.SeedTenant(t, q, "RaceCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	resend := func(id int64) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", id), nil, owner.ID, tenant.ID, "owner")
	}

	t.Run("accepted while the email is out", func(t *testing.T) {
		inv := seedInvite(t, q, tenant.ID, owner.ID, "racer1@test.com", "linguist")
		fake.onSend = func() {
			if _, err := q.AcceptInvite(ctx, inv.ID); err != nil {
				t.Errorf("accept: %v", err)
			}
		}
		defer func() { fake.onSend = nil }()
		testutil.AssertStatus(t, resend(inv.ID), http.StatusConflict)
		if got := getInvite(t, q, tenant.ID, inv.ID).Token; got != inv.Token {
			t.Errorf("token changed to %q although the invite was accepted meanwhile", got)
		}
	})

	t.Run("resent by someone else while the email is out", func(t *testing.T) {
		inv := seedInvite(t, q, tenant.ID, owner.ID, "racer2@test.com", "linguist")
		const theirs = "token-from-the-other-resend"
		fake.onSend = func() {
			if _, err := q.ResendInvite(ctx, db.ResendInviteParams{
				NewToken: theirs, ExpiresAt: inv.ExpiresAt, ID: inv.ID, TenantID: tenant.ID, OldToken: inv.Token,
			}); err != nil {
				t.Errorf("the other resend: %v", err)
			}
		}
		defer func() { fake.onSend = nil }()
		testutil.AssertStatus(t, resend(inv.ID), http.StatusConflict)
		if got := getInvite(t, q, tenant.ID, inv.ID).Token; got != theirs {
			t.Errorf("token = %q, want the other resend's %q", got, theirs)
		}
	})
}

// TestIntegrationInviteOtherTenantCannotTouch: an owner of another org gets
// 404 on resend and revoke, the token stays, and no email goes out.
func TestIntegrationInviteOtherTenantCannotTouch(t *testing.T) {
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
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	ownerA := testutil.SeedUser(t, q, tenantA.ID, "owner-a@test.com", "pass")
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	ownerB := testutil.SeedUser(t, q, tenantB.ID, "owner-b@test.com", "pass")
	inv := seedInvite(t, q, tenantA.ID, ownerA.ID, "a-vendor@test.com", "linguist")

	for _, req := range []struct{ method, path string }{
		{"POST", fmt.Sprintf("/api/invites/%d/resend", inv.ID)},
		{"DELETE", fmt.Sprintf("/api/invites/%d", inv.ID)},
	} {
		rec := testutil.DoRequestWithRole(t, router, req.method, req.path, nil, ownerB.ID, tenantB.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusNotFound)
	}
	if got := getInvite(t, q, tenantA.ID, inv.ID).Token; got != inv.Token {
		t.Errorf("another tenant changed the token to %q", got)
	}
	if fake.calls != 0 {
		t.Errorf("%d emails went out for another tenant's invite", fake.calls)
	}
}

// TestIntegrationInviteResendRevivesExpired: resending an expired invite
// emails a working link with a fresh 7-day expiry.
func TestIntegrationInviteResendRevivesExpired(t *testing.T) {
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
	tenant := testutil.SeedTenant(t, q, "ExpiredCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	token, err := testutil.RandomToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	inv, err := q.CreateInvite(context.Background(), db.CreateInviteParams{
		TenantID: tenant.ID, Email: "late@test.com", Role: "linguist", InvitedBy: owner.ID, Token: token,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("seed expired invite: %v", err)
	}

	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", inv.ID), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	after := getInvite(t, q, tenant.ID, inv.ID)
	if !after.ExpiresAt.Time.After(time.Now().Add(6 * 24 * time.Hour)) {
		t.Errorf("expiry after resend = %v, want about 7 days from now", after.ExpiresAt.Time)
	}
	lookup := testutil.DoRequest(t, router, "GET", "/api/invites/lookup/"+after.Token, nil, owner.ID, tenant.ID)
	testutil.AssertStatus(t, lookup, http.StatusOK)
}

// TestIntegrationInviteEmailLimit: one person triggers at most 20 invitation
// emails at once, create and resend together, and then 429 until the bucket
// refills (one a minute). Another person keeps their own allowance.
func TestIntegrationInviteEmailLimit(t *testing.T) {
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
	tenant := testutil.SeedTenant(t, q, "BulkCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, "admin@test.com", "pass", "admin")
	invite := func(userID int64, role, email string) *httptest.ResponseRecorder {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{"email": email, "role": "linguist"}, userID, tenant.ID, role)
	}

	var first int64
	for i := 0; i < 20; i++ {
		rec := invite(owner.ID, "owner", fmt.Sprintf("bulk-%d@test.com", i))
		testutil.AssertStatus(t, rec, http.StatusCreated)
		if i == 0 {
			var resp struct {
				Data struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &resp)
			first = resp.Data.ID
		}
	}
	// One email comes back every minute, so a client waits at most that long.
	// The exact value shrinks by the time elapsed since the first create.
	assertRetryAfter := func(label string, rec *httptest.ResponseRecorder) {
		t.Helper()
		got := rec.Header().Get("Retry-After")
		n, err := strconv.Atoi(got)
		if err != nil || n < 1 || n > 60 {
			t.Errorf("%s: Retry-After = %q, want whole seconds in 1..60", label, got)
		}
	}
	over := invite(owner.ID, "owner", "bulk-20@test.com")
	testutil.AssertStatus(t, over, http.StatusTooManyRequests)
	assertRetryAfter("create over budget", over)
	rec := testutil.DoRequestWithRole(t, router, "POST", fmt.Sprintf("/api/invites/%d/resend", first), nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusTooManyRequests)
	assertRetryAfter("resend over budget", rec)
	testutil.AssertStatus(t, invite(admin.ID, "admin", "admins-own@test.com"), http.StatusCreated)
	if fake.calls != 21 {
		t.Errorf("sent %d emails, want 21 (20 for the owner, 1 for the admin)", fake.calls)
	}
}

// TestIntegrationInviteWithoutSenderSpendsNoBudget: with no email sender, an
// invite sends no email, so it spends nothing from the email budget.
func TestIntegrationInviteWithoutSenderSpendsNoBudget(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := api.NewRouter(api.Deps{
		Queries:   db.New(pool),
		DB:        pool,
		JWTSecret: testutil.TestJWTSecret,
		BaseURL:   "http://localhost:5173",
	})
	tenant := testutil.SeedTenant(t, q, "QuietCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	for i := range 25 {
		rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites",
			map[string]string{"email": fmt.Sprintf("quiet-%d@test.com", i), "role": "linguist"}, owner.ID, tenant.ID, "owner")
		testutil.AssertStatus(t, rec, http.StatusCreated)
	}
}

// revokeFails is a database whose invite delete always fails.
type revokeFails struct{ db.Querier }

func (revokeFails) DeleteInvite(context.Context, db.DeleteInviteParams) error {
	return errors.New("connection reset")
}

// TestIntegrationInviteFailureLogsCarryInviteID: a failed resend email and a
// failed revoke are logged with the invite id, so the row can be found; the
// failed resend also with the address.
func TestIntegrationInviteFailureLogsCarryInviteID(t *testing.T) {
	var logs bytes.Buffer
	prev := logging.L()
	logging.SetLogger(logging.New(logging.Options{ECS: true, Writer: &logs, Level: slog.LevelDebug, Version: "testrev"}))
	t.Cleanup(func() { logging.SetLogger(prev) })

	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := api.NewRouter(api.Deps{
		Queries:     revokeFails{db.New(pool)},
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: &fakeSender{fail: errors.New("mail server down")},
	})
	tenant := testutil.SeedTenant(t, q, "LogCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")
	inv := seedInvite(t, q, tenant.ID, owner.ID, "logged@test.com", "linguist")

	path := fmt.Sprintf("/api/invites/%d", inv.ID)
	testutil.AssertStatus(t, testutil.DoRequestWithRole(t, router, "POST", path+"/resend", nil, owner.ID, tenant.ID, "owner"), http.StatusBadGateway)
	testutil.AssertStatus(t, testutil.DoRequestWithRole(t, router, "DELETE", path, nil, owner.ID, tenant.ID, "owner"), http.StatusInternalServerError)

	want := strconv.FormatInt(inv.ID, 10)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		action, _ := m[logging.KeyEventAction].(string)
		if action != "invite.resend_email_failed" && action != "invite.delete_failed" {
			continue
		}
		seen[action] = true
		if got := m[logging.KeyInviteID]; got != want {
			t.Errorf("%s logged invite id %v, want %q", action, got, want)
		}
		// The send error leaves the recipient out, and revoking an invite
		// deletes its row, so the log line is the only record of who missed it.
		if got := m[logging.LabelPrefix+"invite_email"]; action == "invite.resend_email_failed" && got != "logged@test.com" {
			t.Errorf("%s logged invite_email %v, want %q", action, got, "logged@test.com")
		}
	}
	for _, action := range []string{"invite.resend_email_failed", "invite.delete_failed"} {
		if !seen[action] {
			t.Errorf("no %s log line:\n%s", action, logs.String())
		}
	}
}

// TestIntegrationInviteEmailFailureNoticeHasNoAddress: a failed invite email is
// reported to error tracking, a third party, so the notice must not carry the
// recipient address that a mail provider's error message can quote.
func TestIntegrationInviteEmailFailureNoticeHasNoAddress(t *testing.T) {
	notices := errtracktest.Install(t)
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	fake := &fakeSender{fail: errors.New("gmail: RCPT TO victim@test.com: mailbox unavailable")}
	router := api.NewRouter(api.Deps{
		Queries:     db.New(pool),
		DB:          pool,
		JWTSecret:   testutil.TestJWTSecret,
		BaseURL:     "http://localhost:5173",
		EmailSender: fake,
	})
	tenant := testutil.SeedTenant(t, q, "NoticeCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@test.com", "pass")

	rec := testutil.DoRequestWithRole(t, router, "POST", "/api/invites", map[string]string{"email": "victim@test.com", "role": "linguist"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	n := notices.One(t)
	if strings.Contains(n.ErrorMessage, "victim@test.com") {
		t.Errorf("the notice quotes the recipient address: %q", n.ErrorMessage)
	}
	if !strings.Contains(n.ErrorMessage, "mailbox unavailable") {
		t.Errorf("the notice lost the provider's reason: %q", n.ErrorMessage)
	}
}
