package notification

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
)

// Service handles notification creation, preference checks, and email dispatch.
// Two-layer model:
//  1. Org-level rules (admin master switches) — if disabled, notification is suppressed entirely
//  2. User-level preferences (personal mute) — user can mute notifications the org has enabled
type Service struct {
	queries db.Querier
	sender  Sender
	// Optional broker for real-time delivery.
	// nil disables live publish; the in-app row + email still fire.
	// Tests that don't care about live delivery pass nil; the server
	// wires a PGBroker.
	broker Broker
	// baseURL is the app's origin, for the links in emails. Empty sends
	// emails without them.
	baseURL string
}

func NewService(q db.Querier, sender Sender) *Service {
	return &Service{queries: q, sender: sender}
}

// NewServiceWithBroker is the production-mode constructor. The broker is
// invoked after the in-app row + email both succeed (or after the email
// step is skipped/fails, since the row write owns the source-of-truth).
// Failure to publish is logged but never propagates — broker downtime
// must not break notification creation. baseURL is the app's origin, for the
// links in emails.
func NewServiceWithBroker(q db.Querier, sender Sender, broker Broker, baseURL string) *Service {
	return &Service{queries: q, sender: sender, broker: broker, baseURL: baseURL}
}

// Send creates an in-app notification and optionally sends an email.
//
// Ordering invariant (load-bearing for the deadline cron's dedup):
//
//  1. Check org rules + user prefs (no DB writes; bail out cheap if disabled).
//  2. Create the in-app notification row with `emailed = false`. After this
//     point the dedup window owns this (recipient, reference) tuple — the
//     deadline cron's `NOT EXISTS` subquery will skip it for 20 hours.
//  3. Send the email. If it fails, log + continue: the user gets the in-app
//     notification but no email; same outcome as `shouldEmail = false`.
//  4. On success, flip the `emailed` flag with `UpdateNotificationEmailed`.
//     If THIS update fails, the user has the email + the row, but the flag
//     stays `false`. Cosmetically wrong but functionally harmless — no
//     duplicate email next hour because the row exists.
//
// Why row-first, email-second: a failure after SendEmail can't lose the
// dedup row, so the next cron firing never sends a SECOND email. The worst
// case is "user gets email but `emailed=false` flag", which is observable
// only in admin tooling, not in user behaviour.
func (s *Service) Send(ctx context.Context, e Event) error {
	subject := Subject(e)
	preview := BodyPreview(e)

	// Layer 1: Org-level rules (admin master switches)
	// If the org has disabled this event type, skip entirely (no in-app, no email)
	orgEnabled := true
	if !e.Type.skipsOrgRules() {
		if orgRules, err := s.queries.GetOrgNotificationRules(ctx, e.TenantID); err == nil {
			switch e.Type {
			case EventAssignment:
				orgEnabled = orgRules.NotifyAssignment
			case EventRejection:
				orgEnabled = orgRules.NotifyRejection
			case EventCompletion:
				orgEnabled = orgRules.NotifyCompletion
			case EventDeadline:
				orgEnabled = orgRules.NotifyDeadline
			}
		}
	}
	// If no org rules row exists, defaults are all true — enabled.

	if !orgEnabled {
		return nil // admin disabled this event type — suppress entirely
	}

	// Layer 2: User-level preferences (personal mute — only controls email, not in-app)
	shouldEmail := !e.Type.bellOnly()
	if shouldEmail {
		if prefs, err := s.queries.GetNotificationPreferences(ctx, e.RecipientID); err == nil {
			switch e.Type {
			case EventAssignment:
				shouldEmail = prefs.EmailAssignment
			case EventRejection:
				shouldEmail = prefs.EmailRejection
			case EventCompletion:
				shouldEmail = prefs.EmailCompletion
			case EventDeadline:
				shouldEmail = prefs.EmailDeadline
			case EventIssueUpdated:
				shouldEmail = prefs.EmailIssueUpdates
			}
		}
	}
	// If no preferences row exists, all defaults are true — send email.

	// Step 2: Create in-app notification row FIRST (with emailed=false).
	// This claims the dedup slot before any external HTTP call.
	refType, refID := reference(e)

	row, err := s.queries.CreateNotification(ctx, db.CreateNotificationParams{
		TenantID:      e.TenantID,
		UserID:        e.RecipientID,
		EventType:     string(e.Type),
		Subject:       subject,
		BodyPreview:   preview,
		ReferenceType: refType,
		ReferenceID:   refID,
		Emailed:       false,
	})
	if err != nil {
		logging.Error(ctx, err, "notification record not created", "notification-create-failed", logging.UserID(e.RecipientID))
		return err
	}
	// From this point on the row exists. Every subsequent failure must
	// be non-fatal AND every return path AFTER this defer registration
	// must hit Step 5 (broker publish). Defer is registered HERE, so
	// the row-creation-failed early return above does NOT trigger
	// publishLive — which is correct: no row, nothing to publish about.
	// Any future early return added BELOW this line is automatically
	// covered by the defer.
	defer s.publishLive(ctx, e)

	// Step 3: send email. Failures DON'T propagate — the in-app row already
	// exists, so the user has been notified in some form, and dedup is owned.
	if !shouldEmail {
		return nil
	}
	user, err := s.queries.GetUser(ctx, e.RecipientID)
	if err != nil {
		logging.Warn(ctx, err, "notification email skipped: recipient lookup failed", "notification-recipient-lookup-failed", logging.UserID(e.RecipientID))
		return nil
	}
	body, err := RenderHTML(e, s.baseURL)
	if err != nil {
		logging.Error(ctx, err, "notification email render failed", "notification-render-failed", logging.UserID(e.RecipientID))
		return nil
	}
	if err := s.sender.SendEmail(ctx, user.Email, EmailSubject(e), body.InThread(e.Thread)); err != nil {
		logging.Error(ctx, err, "notification email send failed", "notification-email-failed", logging.UserID(e.RecipientID))
		// SendFailure strips the address from the provider's message. Event is
		// the notification type, so a broken deadline digest groups apart from
		// a broken assignment notice.
		errtrack.Capture(ctx, errtrack.SendFailure(err, user.Email), errtrack.CaptureOptions{
			Class:     errtrack.ClassEmailSendFailed,
			Message:   "failed to send notification email",
			Subsystem: "email",
			Event:     "notification-" + string(e.Type),
		})
		return nil
	}

	// Step 4: mark the row as emailed. Failure here is cosmetic — flag stays
	// false, dedup still works. Don't propagate.
	if err := s.queries.UpdateNotificationEmailed(ctx, row.ID); err != nil {
		logging.Warn(ctx, err, "notification not marked as emailed", "notification-mark-emailed-failed", logging.UserID(e.RecipientID))
	}

	return nil
}

// reference is what a notification points at: the issue, the job, or the
// project.
func reference(e Event) (refType string, refID int64) {
	switch {
	case e.IssueID > 0:
		return "issue", e.IssueID
	case e.JobID == 0 && e.ProjectID > 0:
		return "project", e.ProjectID
	}
	return "job", e.JobID
}

// SendBells rings the bell of every recipient for one bell-only event: one
// insert for all of them, then each one's open tabs are told. e.RecipientID
// is ignored. Use it for a fan-out to many users, where a Send per user would
// grow with the org.
func (s *Service) SendBells(ctx context.Context, e Event, recipients []int64) error {
	if !e.Type.bellOnly() {
		return fmt.Errorf("notification: %s is not a bell-only event", e.Type)
	}
	if len(recipients) == 0 {
		return nil
	}
	refType, refID := reference(e)
	if err := s.queries.CreateBellNotifications(ctx, db.CreateBellNotificationsParams{
		TenantID: e.TenantID, UserIds: recipients, EventType: string(e.Type),
		Subject: Subject(e), BodyPreview: BodyPreview(e), ReferenceType: refType, ReferenceID: refID,
	}); err != nil {
		return fmt.Errorf("create bell notifications: %w", err)
	}
	for _, id := range recipients {
		e.RecipientID = id
		s.publishLive(ctx, e)
	}
	return nil
}

// publishLive is the Step-5 broker publish, invoked via `defer` from Send
// after the row write succeeds, and once per recipient from SendBells. Broker
// is optional (nil-safe) so tests that don't wire one still work; broker
// errors are logged but never propagated — broker downtime must not break
// notification creation.
//
// Why deferred in Send: every early return between "row created" and
// "function end" needs to publish too (muted-email user, GetUser failure,
// RenderHTML failure, SendEmail failure — the user still has the in-app row
// and the live stream should reflect it). A single defer enforces that
// without duplicating the publish call.
//
// Panic safety: a deferred function that panics replaces the function's
// return value or compounds an in-flight panic. Without recover, a broker
// bug would crash the request goroutine even though the user-visible work
// (CreateNotification, SendEmail) already succeeded. recover() converts the
// panic to a log line.
func (s *Service) publishLive(ctx context.Context, e Event) {
	if s.broker == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			logging.Error(ctx, fmt.Errorf("%v", rec), "notification broker publish panicked", "notification-broker-panic")
			// Recovering keeps the request alive, which is the point — but a
			// panic is still a bug, and the log line alone means nobody hears
			// about it.
			errtrack.CapturePanic(ctx, rec, "notification", "broker-publish")
		}
	}()
	if err := s.broker.Publish(ctx, e); err != nil {
		logging.Warn(ctx, err, "notification broker publish failed", "notification-broker-publish-failed")
	}
}
