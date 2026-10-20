package notification

import (
	"time"

	"openloc/internal/emaillayout"
)

// EventType represents a notification trigger.
type EventType string

const (
	EventAssignment EventType = "assignment"
	EventRejection  EventType = "rejection"
	EventCompletion EventType = "completion"
	EventDeadline   EventType = "deadline"

	// Failure events are bell only: no org rule, no email.
	EventJobFailed EventType = "job_failed"
	EventMTFailed  EventType = "mt_failed"

	// An operator commented on or changed the status of an issue the
	// recipient reported. Personal: no org rule, but the user may mute the email.
	EventIssueUpdated EventType = "issue_updated"
	// The support team resolved an issue of the recipient's org, reported by
	// someone else. For the org's owners, admins and project managers; bell only.
	EventIssueResolved EventType = "issue_resolved"
)

// bellOnly reports whether an event skips the org rules and email.
func (t EventType) bellOnly() bool {
	return t == EventJobFailed || t == EventMTFailed || t == EventIssueResolved
}

// skipsOrgRules reports whether the org's admin switches do not apply.
func (t EventType) skipsOrgRules() bool {
	return t.bellOnly() || t == EventIssueUpdated
}

// Event carries all context needed to render and deliver a notification.
type Event struct {
	Type        EventType
	TenantID    int64
	RecipientID int64  // user to notify
	ActorID     int64  // user who triggered (0 for system/deadline)
	ActorName   string // display name of the actor
	JobID       int64
	ProjectID   int64
	ProjectName string
	FileName    string
	TargetLang  string
	StepName    string
	Reason      string    // rejection reason, what a failure says, or an operator's comment
	Comment     string    // assignment: the hand-off note the previous step left
	DeadlineAt  time.Time // for deadline events

	// Issue events.
	IssueID     int64
	IssueTitle  string
	IssueStatus string // the new status's label, when the status changed
	Link        string // absolute URL the email links to

	// Thread puts the email in a conversation with the other emails about
	// the same thing; the zero Thread is none.
	Thread emaillayout.Thread
}
