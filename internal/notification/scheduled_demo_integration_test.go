package notification_test

import (
	"context"
	"testing"
	"time"

	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

// The demo organization (tenants.kind = 'demo') is never
// scanned: its assignees are synthetic users. The same job reminds once the
// tenant is a customer again, so the kind filter is what kept it out.
func TestScheduled_SkipsDemoOrganization(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	userID, _ := seedDeadlineFixture(t, q, 12*time.Hour)
	memberships, err := q.ListMembershipsWithTenant(ctx, userID)
	if err != nil || len(memberships) != 1 {
		t.Fatalf("ListMembershipsWithTenant: %v (%d rows)", err, len(memberships))
	}
	tenantID := memberships[0].TenantID
	setKind := func(kind string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE tenants SET kind = $2 WHERE id = $1`, tenantID, kind); err != nil {
			t.Fatalf("set tenant kind %s: %v", kind, err)
		}
	}

	sender := &spySender{}
	handler := notif.NewScheduledHandler(q, notif.NewService(q, sender))

	setKind("demo")
	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("HandleDeadlineCheck (demo): %v", err)
	}
	if got := len(sender.Calls()); got != 0 {
		t.Errorf("demo organization: %d deadline emails sent, want 0", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userID); got != 0 {
		t.Errorf("demo organization: %d deadline notifications, want 0", got)
	}

	setKind("customer")
	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("HandleDeadlineCheck (customer): %v", err)
	}
	if got := len(sender.Calls()); got != 1 {
		t.Errorf("customer organization: %d deadline emails sent, want 1", got)
	}
}
