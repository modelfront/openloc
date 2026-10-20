package notification_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/errtrack/errtracktest"
	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

// rcptRefusingSender fails the way an SMTP server refuses a recipient: the
// error quotes the address.
type rcptRefusingSender struct{}

func (rcptRefusingSender) SendEmail(_ context.Context, to, _ string, _ emaillayout.Email) error {
	return fmt.Errorf("gmail: RCPT TO %s: 550 5.1.1 mailbox unavailable", to)
}

// A failed notification email is reported with the provider's reason, but the
// recipient's address stays out of the notice: notices leave our
// infrastructure.
func TestIntegrationEmailFailureNoticeHasNoAddress(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	notices := errtracktest.Install(t)

	tenant := testutil.SeedTenant(t, q, "MailCorp")
	recipient := testutil.SeedUser(t, q, tenant.ID, "recipient@mailcorp.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Mail Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "letter.docx", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	svc := notif.NewService(q, rcptRefusingSender{})
	err := svc.Send(context.Background(), notif.Event{
		Type: notif.EventAssignment, TenantID: tenant.ID, RecipientID: recipient.ID,
		ActorName: "Alice", JobID: job.ID, ProjectID: project.ID, ProjectName: project.Name,
		FileName: file.Name, TargetLang: "de", StepName: "Review",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	n := notices.One(t)
	if !strings.Contains(n.ErrorMessage, "550 5.1.1 mailbox unavailable") {
		t.Errorf("notice lost the provider's reason: %q", n.ErrorMessage)
	}
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), recipient.Email) {
		t.Errorf("the notice carries the recipient's address:\n%s", raw)
	}
}
