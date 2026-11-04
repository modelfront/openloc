package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// TestIntegrationVendorAdminNotice: when a vendor org makes one of its people
// an admin, the client's owners and admins hear about it in the bell, and
// nobody else does. Other role changes ring no bell.
func TestIntegrationVendorAdminNotice(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	router, outbox := testutil.NewTestRouterWithIssues(pool, nil, nil)

	client := testutil.SeedTenant(t, q, "Client Corp")
	owner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pass123")
	admin := testutil.SeedUserWithRole(t, q, client.ID, "admin@client.test", "pass123", auth.RoleAdmin)
	pm := testutil.SeedUserWithRole(t, q, client.ID, "pm@client.test", "pass123", auth.RoleProjectManager)
	clientLing := testutil.SeedUserWithRole(t, q, client.ID, "ling@client.test", "pass123", auth.RoleLinguist)

	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	vadmin := testutil.SeedUserWithRole(t, q, vendor.ID, "boss@acme.test", "pass123", auth.RoleAdmin)
	vling := testutil.SeedUserWithRole(t, q, vendor.ID, "lina@acme.test", "pass123", auth.RoleLinguist)
	vpm := testutil.SeedUserWithRole(t, q, vendor.ID, "pat@acme.test", "pass123", auth.RoleProjectManager)

	// Another client with its own vendor: never hears about Acme.
	other := testutil.SeedTenant(t, q, "Other Client")
	otherOwner := testutil.SeedUser(t, q, other.ID, "owner@other.test", "pass123")

	bells := func(tenantID, userID int64) []db.Notification {
		t.Helper()
		rows, err := q.ListNotifications(ctx, db.ListNotificationsParams{UserID: userID, TenantID: tenantID, PageLimit: 50})
		if err != nil {
			t.Fatalf("ListNotifications: %v", err)
		}
		return rows
	}
	setRole := func(actor db.User, tenantID int64, actorRole string, target db.User, role string) {
		t.Helper()
		rec := testutil.DoRequestWithRole(t, router, http.MethodPut, fmt.Sprintf("/api/members/%d/role", target.ID),
			map[string]string{"role": role}, actor.ID, tenantID, actorRole)
		testutil.AssertStatus(t, rec, http.StatusOK)
	}

	// Changes that make no new vendor admin ring no bell.
	setRole(vadmin, vendor.ID, auth.RoleAdmin, vling, auth.RoleProjectManager)
	setRole(owner, client.ID, auth.RoleOwner, clientLing, auth.RoleAdmin)
	for _, u := range []db.User{owner, admin} {
		if n := bells(client.ID, u.ID); len(n) != 0 {
			t.Fatalf("%s got %d notifications before any vendor admin was added: %+v", u.Email, len(n), n)
		}
	}

	setRole(vadmin, vendor.ID, auth.RoleAdmin, vling, auth.RoleAdmin)

	for _, u := range []db.User{owner, admin, clientLing} {
		rows := bells(client.ID, u.ID)
		if len(rows) != 1 {
			t.Fatalf("%s: want 1 notification, got %d", u.Email, len(rows))
		}
		n := rows[0]
		if n.EventType != "vendor_admin_added" || n.ReferenceType != "vendor_org" || n.ReferenceID != vendor.ID || n.Emailed {
			t.Errorf("%s: notification = %+v", u.Email, n)
		}
		if !strings.Contains(n.Subject, "Acme Vendors") || !strings.Contains(n.Subject+n.BodyPreview, "lina@acme.test") {
			t.Errorf("%s: subject %q / body %q should name the vendor and the new admin", u.Email, n.Subject, n.BodyPreview)
		}
	}
	if rows := bells(client.ID, pm.ID); len(rows) != 0 {
		t.Errorf("a client project manager got %d notifications", len(rows))
	}
	for _, u := range []db.User{vadmin, vling, vpm} {
		if rows := bells(vendor.ID, u.ID); len(rows) != 0 {
			t.Errorf("vendor member %s got %d notifications", u.Email, len(rows))
		}
	}
	if rows := bells(other.ID, otherOwner.ID); len(rows) != 0 {
		t.Errorf("another client's owner got %d notifications", len(rows))
	}
	for _, u := range []db.User{owner, admin, clientLing} {
		if got := len(outbox.For(u.Email)); got != 0 {
			t.Errorf("%s got %d emails for a bell-only notice", u.Email, got)
		}
	}

	// Already an admin: saving the same role again rings no second bell.
	setRole(vadmin, vendor.ID, auth.RoleAdmin, vling, auth.RoleAdmin)
	if rows := bells(client.ID, owner.ID); len(rows) != 1 {
		t.Errorf("re-saving admin rang again: %d notifications", len(rows))
	}
}
