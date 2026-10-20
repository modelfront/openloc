package notification_test

import (
	"context"
	"testing"

	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

// SendBells rings each recipient's bell once, with the same row Send would
// write, and sends no email; nobody else gets one.
func TestSendBells(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "BellCorp")
	a := testutil.SeedUser(t, q, tenant.ID, "a@bellcorp.test", "pw")
	b := testutil.SeedUser(t, q, tenant.ID, "b@bellcorp.test", "pw")
	bystander := testutil.SeedUser(t, q, tenant.ID, "c@bellcorp.test", "pw")

	sender := &spySender{}
	svc := notif.NewService(q, sender)
	e := notif.Event{Type: notif.EventIssueResolved, TenantID: tenant.ID, IssueID: 77, IssueTitle: "Export does nothing"}
	if err := svc.SendBells(ctx, e, []int64{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{a.ID, b.ID} {
		rows := listFor(t, q, tenant.ID, u)
		if len(rows) != 1 {
			t.Fatalf("user %d: want 1 notification, got %d", u, len(rows))
		}
		n := rows[0]
		if n.EventType != string(notif.EventIssueResolved) || n.ReferenceType != "issue" || n.ReferenceID != 77 ||
			n.Subject != notif.Subject(e) || n.Emailed {
			t.Errorf("user %d: notification = %+v", u, n)
		}
	}
	if rows := listFor(t, q, tenant.ID, bystander.ID); len(rows) != 0 {
		t.Errorf("a user not named got %d notifications", len(rows))
	}
	if len(sender.calls) != 0 {
		t.Errorf("sent %d emails for a bell-only event", len(sender.calls))
	}

	if err := svc.SendBells(ctx, e, nil); err != nil {
		t.Errorf("no recipients: %v", err)
	}
	if err := svc.SendBells(ctx, notif.Event{Type: notif.EventAssignment, TenantID: tenant.ID}, []int64{a.ID}); err == nil {
		t.Error("an emailing event was sent as bells only")
	}
}
