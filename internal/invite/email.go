package invite

import (
	"context"
	"fmt"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/errtrack"
	"openloc/internal/httputil"
	"openloc/internal/logging"
)

// Invitation emails one person may trigger: inviteEmailBurst at once, then one
// every inviteEmailRefill. Create and Resend share the budget, over REST and
// MCP alike, so a loop can't flood a mailbox or use up the shared sender's
// quota.
const (
	inviteEmailBurst  = 20
	inviteEmailRefill = time.Minute
)

// EmailLimiter is the per-user invitation email budget, keyed by user id.
type EmailLimiter = httputil.KeyedLimiter[int64]

// NewEmailLimiter builds an empty budget. User ids come from our own
// database, so the key space is bounded.
func NewEmailLimiter() *EmailLimiter {
	return httputil.NewKeyedLimiter[int64](1/inviteEmailRefill.Seconds(), inviteEmailBurst, 0, "invite")
}

// EmailSender delivers the invite email. Satisfied by notification.Sender
// (SMTPSender or GmailSender, ConsoleSender in dev) — declared locally so the
// invite package doesn't depend on the notification package.
type EmailSender interface {
	SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error
}

// inviteEmail builds the email for an invite to role carrying token: the owner
// email for an owner invite, the member email otherwise.
func (s *Service) inviteEmail(ctx context.Context, tenantID int64, role, token string) (string, emaillayout.Content, error) {
	link := emaillayout.AppURL(s.baseURL, "/invite/"+token)
	if role == auth.RoleOwner {
		tenant, err := s.q.GetTenant(ctx, tenantID)
		if err != nil {
			return "", emaillayout.Content{}, fmt.Errorf("load organization: %w", err)
		}
		subject, content := auth.OwnerInviteEmail(tenant.Name, link)
		return subject, content, nil
	}
	return "You've been invited to OpenLoc", emaillayout.Content{
		Heading: "You're invited",
		Body:    emaillayout.P("You've been invited to join an organization on OpenLoc as a %s.", role),
		Button:  &emaillayout.Button{Label: "Accept invitation", URL: link},
		Notes: emaillayout.Join(
			emaillayout.Note("This invitation expires in 7 days."),
			emaillayout.LinkFallback(link),
		),
	}, nil
}

// send renders content in the shared email layout and sends it.
func (s *Service) send(ctx context.Context, to, subject string, content emaillayout.Content) error {
	body, err := emaillayout.Render(s.baseURL, content)
	if err != nil {
		return fmt.Errorf("render invite email: %w", err)
	}
	return s.sender.SendEmail(ctx, to, subject, body)
}

// emailNewInvite sends the email for a new invite. No-op without a sender. A
// failure is logged and reported but not returned, so a mail outage doesn't
// fail invite creation: the inviter sees success and can resend.
func (s *Service) emailNewInvite(ctx context.Context, inv db.Invite) {
	if s.sender == nil {
		return
	}
	subject, content, err := s.inviteEmail(ctx, inv.TenantID, inv.Role, inv.Token)
	if err == nil {
		err = s.send(ctx, inv.Email, subject, content)
	}
	if err != nil {
		// The address goes in the log only: logs stay in-cluster, notices
		// leave our infrastructure, and a provider's error can quote it.
		logging.Error(ctx, err, "sending the invite email failed", "invite.email_failed",
			logging.Label("invite_email", inv.Email))
		errtrack.Capture(ctx, errtrack.SendFailure(err, inv.Email), errtrack.CaptureOptions{
			Class:     errtrack.ClassEmailSendFailed,
			Message:   "failed to send invite email",
			Subsystem: "email",
			Event:     "tenant-invite",
		})
	}
}
