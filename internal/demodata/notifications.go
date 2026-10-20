package demodata

import (
	"context"
	"fmt"
	"time"

	db "openloc/internal/database/sqlcgen"
)

// EnsureNotificationRules gives the tenant organization notification rules
// with every event on, when it has none. created reports whether it wrote.
func EnsureNotificationRules(ctx context.Context, q db.Querier, tenantID int64) (bool, error) {
	if _, err := q.GetOrgNotificationRules(ctx, tenantID); err == nil {
		return false, nil
	} else if !isNoRows(err) {
		return false, fmt.Errorf("get notification rules: %w", err)
	}
	if _, err := q.UpsertOrgNotificationRules(ctx, db.UpsertOrgNotificationRulesParams{
		TenantID: tenantID, NotifyAssignment: true, NotifyRejection: true, NotifyCompletion: true, NotifyDeadline: true,
	}); err != nil {
		return false, fmt.Errorf("create notification rules: %w", err)
	}
	return true, nil
}

// EnsureNotificationPreferences gives the user notification preferences when
// they have none: every email on when email is true, every email off
// otherwise. created reports whether it wrote.
func EnsureNotificationPreferences(ctx context.Context, q db.Querier, userID int64, email bool) (bool, error) {
	if _, err := q.GetNotificationPreferences(ctx, userID); err == nil {
		return false, nil
	} else if !isNoRows(err) {
		return false, fmt.Errorf("get notification preferences: %w", err)
	}
	if _, err := q.UpsertNotificationPreferences(ctx, db.UpsertNotificationPreferencesParams{
		UserID: userID, EmailAssignment: email, EmailRejection: email, EmailCompletion: email, EmailDeadline: email,
	}); err != nil {
		return false, fmt.Errorf("create notification preferences: %w", err)
	}
	return true, nil
}

// notifications writes the organization's rules, email-off preferences for
// every demo member (they are synthetic and have no inbox), and the in-app
// notifications the owner and the German linguist find in their bell. Rows are
// written already emailed, so nothing ever sends them.
func (b *builder) notifications() error {
	if _, err := EnsureNotificationRules(b.ctx, b.q, b.tenantID); err != nil {
		return err
	}
	for _, id := range b.members() {
		if _, err := b.q.UpsertNotificationPreferences(b.ctx, db.UpsertNotificationPreferencesParams{UserID: id}); err != nil {
			return fmt.Errorf("notification preferences: %w", err)
		}
	}
	for _, set := range []struct {
		user  int64
		specs []notificationSpec
	}{{b.ownerID, guideNotifications}, {b.linguistID, linguistNotifications}} {
		for _, n := range set.specs {
			row, err := b.q.CreateNotification(b.ctx, db.CreateNotificationParams{
				TenantID: b.tenantID, UserID: set.user, EventType: n.event, Subject: n.subject, BodyPreview: n.preview,
				ReferenceType: "job", ReferenceID: n.job(b.built), Emailed: true,
			})
			if err != nil {
				return fmt.Errorf("notification %q: %w", n.subject, err)
			}
			if err := b.exec(`UPDATE notifications SET created_at = $2, read = $3 WHERE id = $1`,
				row.ID, b.daysAgo(n.daysAgo).Add(-2*time.Hour), n.read); err != nil {
				return err
			}
		}
	}
	return nil
}

// members is every distinct demo member id.
func (b *builder) members() []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range []int64{b.ownerID, b.linguistID, b.org.ManagerUserID, b.org.FrenchLinguistUserID} {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
