package notification_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

func listFor(t *testing.T, q db.Querier, tenantID, userID int64) []db.Notification {
	t.Helper()
	rows, err := q.ListNotifications(context.Background(), db.ListNotificationsParams{UserID: userID, TenantID: tenantID, PageLimit: 50})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	return rows
}

// A failed job reaches the person who started it and the project's creator,
// once each, in the bell only.
func TestNotifyJobFailed(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "FailCorp")
	creator := testutil.SeedUser(t, q, tenant.ID, "creator@failcorp.test", "pw")
	starter := testutil.SeedUser(t, q, tenant.ID, "starter@failcorp.test", "pw")
	bystander := testutil.SeedUser(t, q, tenant.ID, "other@failcorp.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Fail Project", "en", []string{"de"})
	if _, err := pool.Exec(ctx, `UPDATE projects SET created_by = $1 WHERE id = $2`, creator.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	file := testutil.SeedFile(t, q, project.ID, "report.docx", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	sender := &spySender{}
	svc := notif.NewService(q, sender)
	const reason = "Machine translation failed: OpenAI is having a problem right now. Try again in a few minutes."

	t.Run("mt failure goes to starter and creator", func(t *testing.T) {
		err := svc.NotifyJobFailed(ctx, notif.JobFailure{TenantID: tenant.ID, JobID: job.ID, StartedBy: &starter.ID, MT: true, Reason: reason})
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range []int64{starter.ID, creator.ID} {
			rows := listFor(t, q, tenant.ID, u)
			if len(rows) != 1 {
				t.Fatalf("user %d: want 1 notification, got %d", u, len(rows))
			}
			n := rows[0]
			if n.EventType != string(notif.EventMTFailed) {
				t.Errorf("event_type = %q, want %q", n.EventType, notif.EventMTFailed)
			}
			if n.Subject != "Machine translation failed: report.docx (de)" {
				t.Errorf("subject = %q", n.Subject)
			}
			if n.BodyPreview != reason {
				t.Errorf("preview = %q, want the job's reason", n.BodyPreview)
			}
			if n.ReferenceType != "job" || n.ReferenceID != job.ID {
				t.Errorf("reference = %s/%d, want job/%d", n.ReferenceType, n.ReferenceID, job.ID)
			}
		}
		if got := listFor(t, q, tenant.ID, bystander.ID); len(got) != 0 {
			t.Errorf("a user who neither started nor created it got %d notifications", len(got))
		}
		if calls := sender.Calls(); len(calls) != 0 {
			t.Errorf("failure notifications are bell only, but %d emails were sent", len(calls))
		}
	})

	t.Run("starter who is the creator gets one", func(t *testing.T) {
		before := len(listFor(t, q, tenant.ID, creator.ID))
		if err := svc.NotifyJobFailed(ctx, notif.JobFailure{TenantID: tenant.ID, JobID: job.ID, StartedBy: &creator.ID, Reason: "boom"}); err != nil {
			t.Fatal(err)
		}
		rows := listFor(t, q, tenant.ID, creator.ID)
		if len(rows) != before+1 {
			t.Fatalf("want exactly one new notification, got %d", len(rows)-before)
		}
		if rows[0].EventType != string(notif.EventJobFailed) || rows[0].Subject != "Job failed: report.docx (de)" {
			t.Errorf("got %q / %q", rows[0].EventType, rows[0].Subject)
		}
	})

	t.Run("no starter goes to the creator only", func(t *testing.T) {
		beforeStarter := len(listFor(t, q, tenant.ID, starter.ID))
		beforeCreator := len(listFor(t, q, tenant.ID, creator.ID))
		if err := svc.NotifyJobFailed(ctx, notif.JobFailure{TenantID: tenant.ID, JobID: job.ID, Reason: "boom"}); err != nil {
			t.Fatal(err)
		}
		if got := len(listFor(t, q, tenant.ID, creator.ID)) - beforeCreator; got != 1 {
			t.Errorf("creator: want 1 new, got %d", got)
		}
		if got := len(listFor(t, q, tenant.ID, starter.ID)) - beforeStarter; got != 0 {
			t.Errorf("starter: want 0 new, got %d", got)
		}
	})

	t.Run("org that switched every rule off still gets failures", func(t *testing.T) {
		if _, err := q.UpsertOrgNotificationRules(ctx, db.UpsertOrgNotificationRulesParams{TenantID: tenant.ID}); err != nil {
			t.Fatal(err)
		}
		before := len(listFor(t, q, tenant.ID, starter.ID))
		if err := svc.NotifyJobFailed(ctx, notif.JobFailure{TenantID: tenant.ID, JobID: job.ID, StartedBy: &starter.ID, Reason: "boom"}); err != nil {
			t.Fatal(err)
		}
		if got := len(listFor(t, q, tenant.ID, starter.ID)) - before; got != 1 {
			t.Errorf("want 1 new, got %d", got)
		}
	})

	t.Run("another tenant's job is refused", func(t *testing.T) {
		other := testutil.SeedTenant(t, q, "OtherCorp")
		err := svc.NotifyJobFailed(ctx, notif.JobFailure{TenantID: other.ID, JobID: job.ID, StartedBy: &starter.ID, Reason: "boom"})
		if err == nil {
			t.Fatal("want an error for a job outside the tenant")
		}
	})
}
