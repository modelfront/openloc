package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"openloc/internal/emaillayout"
	"openloc/internal/errtrack"
	"openloc/internal/errtrack/errtracktest"
)

type failingSender struct{ err error }

func (s failingSender) SendEmail(context.Context, string, string, emaillayout.Email) error {
	return s.err
}

type okSender struct{}

func (okSender) SendEmail(context.Context, string, string, emaillayout.Email) error { return nil }

// TestEmailFailureReported — every caller of sendEmail swallows the error so a
// mail outage can't fail the request, which means the only signal a broken mail
// path produces is this notice.
func TestEmailFailureReported(t *testing.T) {
	rec := errtracktest.Install(t)
	h := &Handler{sender: failingSender{err: errors.New("dial tcp: connection refused")}}

	h.sendEmail(context.Background(), "invitee@customer.example", "You've been invited", emaillayout.Email{}, "tenant-invite")

	n := rec.One(t)
	if n.ErrorClass != errtrack.ClassEmailSendFailed {
		t.Errorf("ErrorClass = %q, want %q", n.ErrorClass, errtrack.ClassEmailSendFailed)
	}
	// The kind must be in the notice: an invite that never arrives blocks
	// onboarding, while a failed "org disabled" notice is cosmetic.
	if !strings.Contains(n.ErrorMessage, "tenant-invite") {
		t.Errorf("ErrorMessage = %q, want the email kind", n.ErrorMessage)
	}
	// And the cause, so the notice distinguishes a refused connection from a
	// rejected credential.
	if !strings.Contains(n.ErrorMessage, "connection refused") {
		t.Errorf("ErrorMessage = %q, want the underlying SMTP error", n.ErrorMessage)
	}
}

// TestEmailFailureNoticeOmitsRecipient is the one that matters.
//
// The log line carries the address on purpose — logs stay in-cluster and you
// need to know who missed their invite. A notice goes to a third party, so the
// address must not be in it: not in the message, not in any context value.
// Honeybadger even has a user_email context convention, and the org's own Python
// wrapper deliberately declines it.
func TestEmailFailureNoticeOmitsRecipient(t *testing.T) {
	const recipient = "invitee@customer.example"

	rec := errtracktest.Install(t)
	// A mail server's reply can quote the recipient.
	h := &Handler{sender: failingSender{err: fmt.Errorf("smtp: RCPT TO: 550 mailbox unavailable: <%s>", recipient)}}

	h.sendEmail(context.Background(), recipient, "Subject", emaillayout.Email{}, "tenant-invite")

	n := rec.One(t)
	if strings.Contains(n.ErrorMessage, recipient) {
		t.Errorf("notice message leaks the recipient address: %q", n.ErrorMessage)
	}
	for k, v := range n.Context {
		if s, ok := v.(string); ok && strings.Contains(s, recipient) {
			t.Errorf("notice context %q leaks the recipient address: %q", k, s)
		}
	}
	// Nor via the subject or body, which can carry an org name.
	if strings.Contains(n.ErrorMessage, "customer.example") {
		t.Errorf("notice message leaks the recipient domain: %q", n.ErrorMessage)
	}
	// The provider's reason stays.
	if !strings.Contains(n.ErrorMessage, "550 mailbox unavailable") {
		t.Errorf("notice message lost the provider's reason: %q", n.ErrorMessage)
	}
}

// An address the pattern cannot recognise (an invite accepts any text) is still
// kept out when the server quotes it back.
func TestEmailFailureNoticeOmitsMalformedRecipient(t *testing.T) {
	const recipient = "john.doe"

	rec := errtracktest.Install(t)
	h := &Handler{sender: failingSender{err: fmt.Errorf("smtp: RCPT TO: 553 5.1.3 The recipient address <%s> is not a valid RFC-5321 address", recipient)}}

	h.sendEmail(context.Background(), recipient, "Subject", emaillayout.Email{}, "tenant-invite")

	n := rec.One(t)
	if strings.Contains(n.ErrorMessage, recipient) {
		t.Errorf("notice message leaks the recipient: %q", n.ErrorMessage)
	}
	if !strings.Contains(n.ErrorMessage, "553 5.1.3") {
		t.Errorf("notice message lost the provider's reason: %q", n.ErrorMessage)
	}
}

func TestEmailSuccessReportsNothing(t *testing.T) {
	rec := errtracktest.Install(t)
	h := &Handler{sender: okSender{}}

	h.sendEmail(context.Background(), "someone@example.com", "Subject", emaillayout.Email{}, "tenant-invite")

	if rec.Len() != 0 {
		t.Errorf("got %d notices for a successful send, want 0", rec.Len())
	}
}
