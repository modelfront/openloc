package notification

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// The caller's own notifications and email preferences, shared by the REST
// handlers and the MCP tools. Every read and write is keyed by the caller's
// user id; notifications are also scoped by tenant.

// Page sizes for List.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Page is one page of notifications, newest first.
type Page struct {
	Notifications []db.Notification
	// NextCursor continues after the last row; HasMore says whether to.
	NextCursor int64
	HasMore    bool
}

// List returns a page of the user's notifications. cursor 0 is the first
// page; limit is clamped to 1..MaxPageSize (0 means DefaultPageSize).
func List(ctx context.Context, q db.Querier, tenantID, userID int64, unreadOnly bool, cursor int64, limit int) (Page, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if cursor < 0 {
		cursor = 0
	}
	rows, err := q.ListNotifications(ctx, db.ListNotificationsParams{
		UserID:     userID,
		TenantID:   tenantID,
		UnreadOnly: unreadOnly,
		Cursor:     cursor,
		PageLimit:  int32(limit),
	})
	if err != nil {
		return Page{}, fmt.Errorf("list notifications: %w", err)
	}
	p := Page{Notifications: rows, HasMore: len(rows) == limit}
	if len(rows) > 0 {
		p.NextCursor = rows[len(rows)-1].ID
	}
	return p, nil
}

// MarkRead marks the given notifications read. Ids that are not the user's
// are skipped by the query's user and tenant filter.
func MarkRead(ctx context.Context, q db.Querier, tenantID, userID int64, ids []int64) error {
	for _, id := range ids {
		if err := q.MarkNotificationRead(ctx, db.MarkNotificationReadParams{ID: id, UserID: userID, TenantID: tenantID}); err != nil {
			return fmt.Errorf("mark notification read: %w", err)
		}
	}
	return nil
}

// MarkAllRead marks every unread notification of the user read.
func MarkAllRead(ctx context.Context, q db.Querier, tenantID, userID int64) error {
	if err := q.MarkAllNotificationsRead(ctx, db.MarkAllNotificationsReadParams{UserID: userID, TenantID: tenantID}); err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}

// UnreadCount is the user's number of unread notifications.
func UnreadCount(ctx context.Context, q db.Querier, tenantID, userID int64) (int64, error) {
	n, err := q.CountUnreadNotifications(ctx, db.CountUnreadNotificationsParams{UserID: userID, TenantID: tenantID})
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return n, nil
}

// Preferences are the user's email switches. A user with no stored row has
// every switch on.
type Preferences struct {
	EmailAssignment bool `json:"email_assignment"`
	EmailRejection  bool `json:"email_rejection"`
	EmailCompletion bool `json:"email_completion"`
	EmailDeadline   bool `json:"email_deadline"`
	// Updates on an issue the user reported: an operator's comment or status change.
	EmailIssueUpdates bool `json:"email_issue_updates"`
}

// PreferencesPatch changes only the switches that are set.
type PreferencesPatch struct {
	EmailAssignment   *bool `json:"email_assignment"`
	EmailRejection    *bool `json:"email_rejection"`
	EmailCompletion   *bool `json:"email_completion"`
	EmailDeadline     *bool `json:"email_deadline"`
	EmailIssueUpdates *bool `json:"email_issue_updates"`
}

// GetPreferences returns the user's switches, all on when none are stored.
func GetPreferences(ctx context.Context, q db.Querier, userID int64) (Preferences, error) {
	row, err := q.GetNotificationPreferences(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preferences{EmailAssignment: true, EmailRejection: true, EmailCompletion: true, EmailDeadline: true, EmailIssueUpdates: true}, nil
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("get notification preferences: %w", err)
	}
	return preferencesFromRow(row), nil
}

func preferencesFromRow(row db.NotificationPreference) Preferences {
	return Preferences{
		EmailAssignment: row.EmailAssignment, EmailRejection: row.EmailRejection,
		EmailCompletion: row.EmailCompletion, EmailDeadline: row.EmailDeadline,
		EmailIssueUpdates: row.EmailIssueUpdates,
	}
}

// UpdatePreferences applies a patch on top of the stored (or default) switches.
func UpdatePreferences(ctx context.Context, q db.Querier, userID int64, patch PreferencesPatch) (Preferences, error) {
	p, err := GetPreferences(ctx, q, userID)
	if err != nil {
		return Preferences{}, err
	}
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&p.EmailAssignment, patch.EmailAssignment)
	set(&p.EmailRejection, patch.EmailRejection)
	set(&p.EmailCompletion, patch.EmailCompletion)
	set(&p.EmailDeadline, patch.EmailDeadline)
	set(&p.EmailIssueUpdates, patch.EmailIssueUpdates)
	row, err := q.UpsertNotificationPreferences(ctx, db.UpsertNotificationPreferencesParams{
		UserID:            userID,
		EmailAssignment:   p.EmailAssignment,
		EmailRejection:    p.EmailRejection,
		EmailCompletion:   p.EmailCompletion,
		EmailDeadline:     p.EmailDeadline,
		EmailIssueUpdates: p.EmailIssueUpdates,
	})
	if err != nil {
		return Preferences{}, fmt.Errorf("update notification preferences: %w", err)
	}
	return preferencesFromRow(row), nil
}
