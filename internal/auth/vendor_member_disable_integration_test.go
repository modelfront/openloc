package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type disabledFixture struct {
	t                                   *testing.T
	client, vendor, other               db.Tenant
	owner, clientAdmin, clientPM        db.User
	otherOwner                          db.User
	vadmin, vadmin2, vpm, vling, vling2 db.User
}

func newDisabledFixture(t *testing.T, q db.Querier) *disabledFixture {
	t.Helper()
	f := &disabledFixture{t: t}
	f.client = testutil.SeedTenant(t, q, "Client Corp")
	f.owner = testutil.SeedUser(t, q, f.client.ID, "owner@client.test", "pw")
	f.clientAdmin = testutil.SeedUserWithRole(t, q, f.client.ID, "admin@client.test", "pw", auth.RoleAdmin)
	f.clientPM = testutil.SeedUserWithRole(t, q, f.client.ID, "pm@client.test", "pw", auth.RoleProjectManager)
	f.other = testutil.SeedTenant(t, q, "Other Client")
	f.otherOwner = testutil.SeedUser(t, q, f.other.ID, "owner@other.test", "pw")
	v := testutil.SeedVendorTenant(t, q, f.client.ID, "Acme Vendors")
	f.vendor = v
	f.vadmin = testutil.SeedUserWithRole(t, q, v.ID, "boss@acme.test", "pw", auth.RoleAdmin)
	f.vadmin2 = testutil.SeedUserWithRole(t, q, v.ID, "boss2@acme.test", "pw", auth.RoleAdmin)
	f.vpm = testutil.SeedUserWithRole(t, q, v.ID, "pat@acme.test", "pw", auth.RoleProjectManager)
	f.vling = testutil.SeedUserWithRole(t, q, v.ID, "lina@acme.test", "pw", auth.RoleLinguist)
	f.vling2 = testutil.SeedUserWithRole(t, q, v.ID, "leo@acme.test", "pw", auth.RoleLinguist)
	return f
}

func setDisabled(t *testing.T, exec func(string, ...any) error, tenantID, userID int64) {
	t.Helper()
	if err := exec(`UPDATE memberships SET disabled_at = now() - interval '1 day', last_active_at = now() - interval '100 days'
		WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

// TestIntegrationVendorMemberDisabled: a vendor org member disabled for disuse
// is refused everywhere in that org and told so by /me; using the org records
// activity once a day; their admin enables any of them, a vendor manager or
// linguist enables nobody, and the client org's owners and admins enable the
// vendor's admins (and only its admins), even while another admin is active.
func TestIntegrationVendorMemberDisabled(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router := testutil.NewTestRouter(pool)
	f := newDisabledFixture(t, q)
	exec := func(sql string, args ...any) error { _, err := pool.Exec(ctx, sql, args...); return err }

	as := func(u db.User, tenantID int64, role, method, path string) int {
		t.Helper()
		return testutil.DoRequestWithRole(t, router, method, path, nil, u.ID, tenantID, role).Code
	}
	vendorAs := func(u db.User, role, method, path string) int { return as(u, f.vendor.ID, role, method, path) }
	membership := func(tenantID, userID int64) db.Membership {
		t.Helper()
		m, err := q.GetMembership(ctx, db.GetMembershipParams{UserID: userID, TenantID: tenantID})
		if err != nil {
			t.Fatalf("GetMembership: %v", err)
		}
		return m
	}

	t.Run("using the org records activity once a day", func(t *testing.T) {
		if err := exec(`UPDATE memberships SET last_active_at = now() - interval '40 days', inactivity_warned_at = now() WHERE user_id = $1`, f.vling.ID); err != nil {
			t.Fatal(err)
		}
		if code := vendorAs(f.vling, auth.RoleLinguist, http.MethodGet, "/api/tasks"); code != http.StatusOK {
			t.Fatalf("GET /api/tasks = %d", code)
		}
		m := membership(f.vendor.ID, f.vling.ID)
		if time.Since(m.LastActiveAt.Time) > time.Minute || m.InactivityWarnedAt.Valid {
			t.Errorf("after use: last_active_at %v, warned %v; want now and no warning", m.LastActiveAt.Time, m.InactivityWarnedAt.Valid)
		}
		if err := exec(`UPDATE memberships SET last_active_at = now() - interval '2 hours' WHERE user_id = $1`, f.vling.ID); err != nil {
			t.Fatal(err)
		}
		if code := vendorAs(f.vling, auth.RoleLinguist, http.MethodGet, "/api/tasks"); code != http.StatusOK {
			t.Fatalf("second GET /api/tasks = %d, want 200", code)
		}
		if m := membership(f.vendor.ID, f.vling.ID); time.Since(m.LastActiveAt.Time) < time.Hour {
			t.Errorf("a second use the same day wrote last_active_at again")
		}
	})

	t.Run("a disabled member is refused and /me says why", func(t *testing.T) {
		setDisabled(t, exec, f.vendor.ID, f.vling.ID)
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/tasks", nil, f.vling.ID, f.vendor.ID, auth.RoleLinguist)
		testutil.AssertStatus(t, rec, http.StatusForbidden)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		testutil.DecodeJSON(t, rec, &body)
		if body.Error.Code != "membership_disabled" {
			t.Errorf("code = %q, want membership_disabled", body.Error.Code)
		}
		if m := membership(f.vendor.ID, f.vling.ID); time.Since(m.LastActiveAt.Time) < 24*time.Hour {
			t.Error("a refused request recorded activity")
		}
		rec = testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/auth/me", nil, f.vling.ID, f.vendor.ID, auth.RoleLinguist)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var me struct {
			Data map[string]any `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &me)
		if me.Data["membership_disabled"] != true {
			t.Errorf("/me = %v, want membership_disabled true", me)
		}
	})

	t.Run("managers and linguists enable nobody", func(t *testing.T) {
		path := fmt.Sprintf("/api/members/%d/enable", f.vling.ID)
		if code := vendorAs(f.vpm, auth.RoleProjectManager, http.MethodPost, path); code != http.StatusForbidden {
			t.Errorf("vendor PM enable linguist = %d, want 403", code)
		}
		if code := vendorAs(f.vling2, auth.RoleLinguist, http.MethodPost, path); code != http.StatusForbidden {
			t.Errorf("vendor linguist enable linguist = %d, want 403", code)
		}
		if !membership(f.vendor.ID, f.vling.ID).DisabledAt.Valid {
			t.Error("the linguist was enabled")
		}
	})

	t.Run("the vendor admin enables a linguist, a manager and another admin", func(t *testing.T) {
		setDisabled(t, exec, f.vendor.ID, f.vpm.ID)
		setDisabled(t, exec, f.vendor.ID, f.vadmin2.ID)
		for _, u := range []db.User{f.vling, f.vpm, f.vadmin2} {
			if code := vendorAs(f.vadmin, auth.RoleAdmin, http.MethodPost, fmt.Sprintf("/api/members/%d/enable", u.ID)); code != http.StatusOK {
				t.Errorf("enable %s = %d, want 200", u.Email, code)
			}
			m := membership(f.vendor.ID, u.ID)
			if m.DisabledAt.Valid || time.Since(m.LastActiveAt.Time) > time.Minute {
				t.Errorf("%s after enable: disabled %v, last_active_at %v; want enabled with the clock restarted", u.Email, m.DisabledAt.Valid, m.LastActiveAt.Time)
			}
		}
		if code := vendorAs(f.vling, auth.RoleLinguist, http.MethodGet, "/api/tasks"); code != http.StatusOK {
			t.Errorf("enabled linguist GET /api/tasks = %d, want 200", code)
		}
		// Nothing to enable: an enabled member, someone of another org.
		if code := vendorAs(f.vadmin, auth.RoleAdmin, http.MethodPost, fmt.Sprintf("/api/members/%d/enable", f.vling.ID)); code != http.StatusNotFound {
			t.Errorf("enable an enabled member = %d, want 404", code)
		}
		// A disabled membership in another org stays out of reach.
		setDisabled(t, exec, f.client.ID, f.clientPM.ID)
		if code := vendorAs(f.vadmin, auth.RoleAdmin, http.MethodPost, fmt.Sprintf("/api/members/%d/enable", f.clientPM.ID)); code != http.StatusNotFound {
			t.Errorf("enable a disabled client member = %d, want 404", code)
		}
		if !membership(f.client.ID, f.clientPM.ID).DisabledAt.Valid {
			t.Error("a vendor admin enabled a membership in its client org")
		}
		if err := exec(`UPDATE memberships SET disabled_at = NULL WHERE tenant_id = $1 AND user_id = $2`, f.client.ID, f.clientPM.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an admin may demote a disabled admin", func(t *testing.T) {
		setDisabled(t, exec, f.vendor.ID, f.vadmin2.ID)
		rec := testutil.DoRequestWithRole(t, router, http.MethodPut, fmt.Sprintf("/api/members/%d/role", f.vadmin2.ID),
			map[string]string{"role": auth.RoleLinguist}, f.vadmin.ID, f.vendor.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		if err := exec(`UPDATE memberships SET role = 'admin' WHERE tenant_id = $1 AND user_id = $2`, f.vendor.ID, f.vadmin2.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the client enables a vendor admin, and only an admin", func(t *testing.T) {
		adminPath := fmt.Sprintf("/api/vendor-orgs/%d/admins/%d/enable", f.vendor.ID, f.vadmin2.ID)
		if code := as(f.clientPM, f.client.ID, auth.RoleProjectManager, http.MethodPost, adminPath); code != http.StatusForbidden {
			t.Errorf("client PM = %d, want 403", code)
		}
		if code := as(f.otherOwner, f.other.ID, auth.RoleOwner, http.MethodPost, adminPath); code != http.StatusNotFound {
			t.Errorf("another client's owner = %d, want 404", code)
		}
		if code := vendorAs(f.vadmin, auth.RoleAdmin, http.MethodPost, adminPath); code != http.StatusForbidden {
			t.Errorf("the vendor itself on the client route = %d, want 403", code)
		}
		if !membership(f.vendor.ID, f.vadmin2.ID).DisabledAt.Valid {
			t.Fatal("the admin was enabled by someone who may not")
		}
		// Another vendor admin is active; the client may enable this one anyway.
		if code := as(f.clientAdmin, f.client.ID, auth.RoleAdmin, http.MethodPost, adminPath); code != http.StatusOK {
			t.Errorf("client admin = %d, want 200", code)
		}
		if membership(f.vendor.ID, f.vadmin2.ID).DisabledAt.Valid {
			t.Error("the vendor admin is still disabled")
		}

		setDisabled(t, exec, f.vendor.ID, f.vling2.ID)
		lingPath := fmt.Sprintf("/api/vendor-orgs/%d/admins/%d/enable", f.vendor.ID, f.vling2.ID)
		if code := as(f.owner, f.client.ID, auth.RoleOwner, http.MethodPost, lingPath); code != http.StatusNotFound {
			t.Errorf("client owner enables a vendor linguist = %d, want 404", code)
		}
	})

	t.Run("lists show who is disabled", func(t *testing.T) {
		setDisabled(t, exec, f.vendor.ID, f.vadmin2.ID)
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/members/page", nil, f.vadmin.ID, f.vendor.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page struct {
			Data []struct {
				Email      string  `json:"email"`
				DisabledAt *string `json:"disabled_at"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &page)
		disabled := map[string]bool{}
		for _, m := range page.Data {
			disabled[m.Email] = m.DisabledAt != nil
		}
		if !disabled[f.vadmin2.Email] || !disabled[f.vling2.Email] || disabled[f.vadmin.Email] || len(page.Data) != 5 {
			t.Errorf("members page disabled = %v, want boss2 and leo disabled, all 5 listed", disabled)
		}

		rec = testutil.DoRequest(t, router, http.MethodGet, "/api/vendor-orgs", nil, f.owner.ID, f.client.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var vendors struct {
			Data []struct {
				Admins []struct {
					UserID   int64  `json:"user_id"`
					Email    string `json:"email"`
					Disabled bool   `json:"disabled"`
				} `json:"admins"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &vendors)
		got, _ := json.Marshal(vendors.Data)
		want := fmt.Sprintf(`[{"admins":[{"user_id":%d,"email":"boss2@acme.test","disabled":true},{"user_id":%d,"email":"boss@acme.test","disabled":false}]}]`, f.vadmin2.ID, f.vadmin.ID)
		if string(got) != want {
			t.Errorf("vendors = %s\nwant      %s", got, want)
		}

		rec = testutil.DoRequestWithRole(t, router, http.MethodGet, "/api/auth/orgs", nil, f.vadmin2.ID, f.vendor.ID, auth.RoleAdmin)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var orgs struct {
			Data []struct {
				TenantID   int64   `json:"tenant_id"`
				DisabledAt *string `json:"disabled_at"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &orgs)
		if len(orgs.Data) != 1 || orgs.Data[0].DisabledAt == nil {
			t.Errorf("org picker = %+v, want the vendor org marked disabled", orgs.Data)
		}
	})
}
