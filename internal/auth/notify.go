package auth

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
)

// appLink builds an absolute URL into the SPA from a path.
func (h *Handler) appLink(path string) string {
	return emaillayout.AppURL(h.baseURL, path)
}

// notifyOperatorsOfSignup emails every configured platform operator that a new
// org is awaiting approval. Best-effort — logs and continues on any failure so
// it never blocks the signup request. No-op if no sender / no operators wired.
func (h *Handler) notifyOperatorsOfSignup(ctx context.Context, tenant db.Tenant, owner db.User) {
	if h.sender == nil || len(h.platformAdmins) == 0 {
		return
	}
	domain := ""
	if tenant.Domain != nil {
		domain = *tenant.Domain
	}
	subject := fmt.Sprintf("New org signup pending approval: %s", tenant.Name)
	content := emaillayout.Content{
		Heading: "New organization awaiting approval",
		Body:    emaillayout.P("A new organization is awaiting your approval."),
		Details: []emaillayout.Row{
			{Label: "Organization", Value: tenant.Name},
			{Label: "Domain", Value: domain},
			{Label: "Requested by", Value: fmt.Sprintf("%s (%s)", owner.Name, owner.Email)},
		},
		Button: &emaillayout.Button{Label: "Review organizations", URL: h.appLink("/platform/orgs")},
	}
	for email := range h.platformAdmins {
		h.sendLayoutEmail(ctx, email, subject, content, "signup-operator-notice")
	}
}

// OwnerInviteEmail is the owner invite for a platform-created org. It differs
// from the member invite: the recipient will OWN the org, which a platform
// operator set up for them. The invite package sends it again on a resend.
func OwnerInviteEmail(orgName, link string) (subject string, content emaillayout.Content) {
	subject = fmt.Sprintf("You're invited to be the owner of %s on OpenLoc", orgName)
	content = emaillayout.Content{
		Heading: fmt.Sprintf("You're invited to own %s", orgName),
		Body: emaillayout.Join(
			emaillayout.P("You've been invited to be the owner of %s on OpenLoc.", emaillayout.Strong(orgName)),
			emaillayout.P("You can set a password or just sign in with an SSO provider like Microsoft or Google."),
		),
		Button: &emaillayout.Button{Label: "Accept invitation", URL: link},
		Notes: emaillayout.Join(
			emaillayout.Note("This invitation expires in 7 days."),
			emaillayout.LinkFallback(link),
		),
	}
	return subject, content
}

// notifyInvitedOwner emails the owner invite for a platform-created org.
func (h *Handler) notifyInvitedOwner(ctx context.Context, ownerEmail, orgName, token string) {
	if h.sender == nil {
		return
	}
	subject, content := OwnerInviteEmail(orgName, h.appLink("/invite/"+token))
	h.sendLayoutEmail(ctx, ownerEmail, subject, content, "owner-invite")
}

// sendLayoutEmail renders content in the shared email layout and sends it.
func (h *Handler) sendLayoutEmail(ctx context.Context, to, subject string, content emaillayout.Content, kind string) {
	body, err := emaillayout.Render(h.baseURL, content)
	if err != nil {
		h.reportEmailFailure(ctx, to, kind, err)
		return
	}
	h.sendEmail(ctx, to, subject, body, kind)
}

// sendEmail delivers one email, reporting a failure rather than only logging it.
//
// Every caller deliberately swallows the error — SMTP being down must not fail
// the request that triggered the email — so the user gets HTTP 200 while the
// recipient gets nothing. This notice is the only signal that a mail path is
// broken.
//
// `kind` is a stable slug naming which email it was, so failures group usefully:
// an invite that never arrives blocks onboarding, while an "org disabled" notice
// failing is cosmetic, and one should not hide the other.
//
// The recipient address goes in the LOG only. Logs stay in-cluster; a notice goes
// to a third party (see internal/errtrack), so it carries the kind plus whatever
// tenant and user IDs the scope already holds — never the address.
func (h *Handler) sendEmail(ctx context.Context, to, subject string, body emaillayout.Email, kind string) {
	if err := h.sender.SendEmail(ctx, to, subject, body); err != nil {
		h.reportEmailFailure(ctx, to, kind, err)
	}
}

func (h *Handler) reportEmailFailure(ctx context.Context, to, kind string, err error) {
	logging.Error(ctx, err, "email send failed", "auth-email-failed", logging.Label("email_kind", kind))
	errtrack.Capture(ctx, errtrack.SendFailure(err, to), errtrack.CaptureOptions{
		Class:     errtrack.ClassEmailSendFailed,
		Message:   "failed to send " + kind + " email",
		Subsystem: "email",
		Event:     kind,
	})
}

// notifyOwnerApproved emails the org owner that their signup was approved.
func (h *Handler) notifyOwnerApproved(ctx context.Context, ownerEmail, orgName string) {
	if h.sender == nil {
		return
	}
	subject := fmt.Sprintf("Your organization %q is approved", orgName)
	content := emaillayout.Content{
		Heading: "Your organization is approved",
		Body: emaillayout.Join(
			emaillayout.P("Good news — %s has been approved on OpenLoc.", emaillayout.Strong(orgName)),
			emaillayout.P("You can now sign in, invite your team, and configure your organization."),
		),
		Button: &emaillayout.Button{Label: "Sign in", URL: h.appLink("/")},
	}
	h.sendLayoutEmail(ctx, ownerEmail, subject, content, "signup-approved")
}

// notifyOwnerDisabled emails the org owner that their (previously active) org
// has been disabled by an operator.
func (h *Handler) notifyOwnerDisabled(ctx context.Context, ownerEmail, orgName string) {
	if h.sender == nil {
		return
	}
	subject := fmt.Sprintf("Your organization %q has been disabled", orgName)
	content := emaillayout.Content{
		Heading: "Your organization has been disabled",
		Body: emaillayout.Join(
			emaillayout.P("%s has been disabled on OpenLoc and is no longer accessible.", emaillayout.Strong(orgName)),
			emaillayout.P("If you think this is a mistake, reply to this email and we'll take a look."),
		),
	}
	h.sendLayoutEmail(ctx, ownerEmail, subject, content, "org-disabled")
}

// notifyOwnerRejected emails the org owner that their signup was declined.
func (h *Handler) notifyOwnerRejected(ctx context.Context, ownerEmail, orgName string) {
	if h.sender == nil {
		return
	}
	subject := fmt.Sprintf("Your organization signup for %q", orgName)
	content := emaillayout.Content{
		Heading: "Your organization signup",
		Body: emaillayout.Join(
			emaillayout.P("Thanks for your interest in OpenLoc. Unfortunately the signup for %s could not be approved at this time.", emaillayout.Strong(orgName)),
			emaillayout.P("If you think this is a mistake, reply to this email and we'll take another look."),
		),
	}
	h.sendLayoutEmail(ctx, ownerEmail, subject, content, "signup-rejected")
}
