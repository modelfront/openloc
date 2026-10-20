package notification

import (
	"fmt"
	"html/template"

	"openloc/internal/emaillayout"
	"openloc/internal/langcode"
)

// Subject returns the email subject line for an event.
func Subject(e Event) string {
	switch e.Type {
	case EventAssignment:
		return fmt.Sprintf("You've been assigned to %s — %s (%s)", e.StepName, e.FileName, e.TargetLang)
	case EventRejection:
		return fmt.Sprintf("Step rejected: %s — %s (%s)", e.StepName, e.FileName, e.TargetLang)
	case EventCompletion:
		return fmt.Sprintf("Workflow completed: %s (%s)", e.FileName, e.TargetLang)
	case EventDeadline:
		return fmt.Sprintf("Deadline approaching: %s (%s)", e.FileName, e.TargetLang)
	case EventMTFailed:
		return fmt.Sprintf("Machine translation failed: %s (%s)", e.FileName, e.TargetLang)
	case EventJobFailed:
		return fmt.Sprintf("Job failed: %s (%s)", e.FileName, e.TargetLang)
	case EventIssueUpdated:
		return fmt.Sprintf("Update on your issue: %s", e.IssueTitle)
	case EventIssueResolved:
		return fmt.Sprintf("Issue resolved: %s", e.IssueTitle)
	default:
		return "OpenLoc notification"
	}
}

// IssueSubject is the subject of every email about one issue, to the support
// team and to the reporter alike: one subject per issue, so mail apps keep
// the issue's emails in one conversation.
func IssueSubject(id int64, title string) string {
	return fmt.Sprintf("Issue #%d: %s", id, title)
}

// EmailSubject is the subject of an event's email. It is the bell's subject,
// except for an issue update, whose email takes the issue's own subject.
func EmailSubject(e Event) string {
	if e.Type == EventIssueUpdated {
		return IssueSubject(e.IssueID, e.IssueTitle)
	}
	return Subject(e)
}

// BodyPreview returns a short plain-text preview for the notification list.
func BodyPreview(e Event) string {
	switch e.Type {
	case EventAssignment:
		preview := fmt.Sprintf("You've been assigned to the \"%s\" step for %s (%s) in project %s.", e.StepName, e.FileName, e.TargetLang, e.ProjectName)
		if e.Comment != "" {
			preview += " Comment: " + e.Comment
		}
		return preview
	case EventRejection:
		if e.Reason != "" {
			return fmt.Sprintf("Your work on \"%s\" for %s (%s) was rejected by %s. Reason: %s", e.StepName, e.FileName, e.TargetLang, e.ActorName, e.Reason)
		}
		return fmt.Sprintf("Your work on \"%s\" for %s (%s) was rejected by %s.", e.StepName, e.FileName, e.TargetLang, e.ActorName)
	case EventCompletion:
		return fmt.Sprintf("All workflow steps completed for %s (%s) in project %s.", e.FileName, e.TargetLang, e.ProjectName)
	case EventDeadline:
		return fmt.Sprintf("Deadline approaching for %s (%s) in project %s — due %s.", e.FileName, e.TargetLang, e.ProjectName, e.DeadlineAt.Format("Jan 2, 2006 3:04 PM"))
	case EventMTFailed, EventJobFailed:
		return e.Reason
	case EventIssueUpdated:
		return issueUpdateSummary(e)
	case EventIssueResolved:
		return "The support team resolved an issue your organization reported."
	default:
		return ""
	}
}

// issueUpdateSummary is one line for the bell: the new status, the comment, or both.
func issueUpdateSummary(e Event) string {
	switch {
	case e.IssueStatus != "" && e.Reason != "":
		return fmt.Sprintf("Status: %s. %s", e.IssueStatus, e.Reason)
	case e.IssueStatus != "":
		return fmt.Sprintf("Status: %s.", e.IssueStatus)
	default:
		return e.Reason
	}
}

// RenderHTML renders the email body for an event. baseURL is the app's
// origin; without it the email has no button or settings link.
func RenderHTML(e Event, baseURL string) (emaillayout.Email, error) {
	if e.Type == EventIssueUpdated {
		return renderIssueUpdate(e, baseURL)
	}
	c := emaillayout.Content{
		Preheader: BodyPreview(e),
		Heading:   heading(e.Type),
		Details: []emaillayout.Row{
			{Label: "Project", Value: e.ProjectName},
			{Label: "File", Value: e.FileName},
			{Label: "Language", Value: langcode.Label(e.TargetLang)},
		},
	}
	buttonLabel := "Open job"
	switch e.Type {
	case EventAssignment:
		c.Body = emaillayout.P("You've been assigned to the %s step.", emaillayout.Strong(e.StepName))
		if e.Comment != "" {
			c.Body = emaillayout.Join(c.Body, emaillayout.Callout("Comment", e.Comment))
		}
		if e.ActorName != "" {
			c.Details = append(c.Details, emaillayout.Row{Label: "Assigned by", Value: e.ActorName})
		}
		buttonLabel = "Open task"
	case EventRejection:
		c.Body = emaillayout.P("The %s step was sent back for revision.", emaillayout.Strong(e.StepName))
		if e.Reason != "" {
			c.Body = emaillayout.Join(c.Body, emaillayout.Callout("Reason", e.Reason))
		}
		if e.ActorName != "" {
			c.Details = append(c.Details, emaillayout.Row{Label: "Rejected by", Value: e.ActorName})
		}
	case EventCompletion:
		c.Body = emaillayout.P("All workflow steps are done for %s.", emaillayout.Strong(e.FileName))
	case EventDeadline:
		c.Body = emaillayout.P("%s is due %s.", emaillayout.Strong(e.FileName), emaillayout.Strong(e.DeadlineAt.Format("January 2, 2006 at 3:04 PM")))
		c.Details = append(c.Details, emaillayout.Row{Label: "Current step", Value: e.StepName})
	}

	if e.ProjectID > 0 && e.JobID > 0 {
		if url := emaillayout.AppURL(baseURL, fmt.Sprintf("/projects/%d/jobs/%d/edit", e.ProjectID, e.JobID)); url != "" {
			c.Button = &emaillayout.Button{Label: buttonLabel, URL: url}
		}
	}
	c.Footer = settingsFooter(baseURL, "Sent by OpenLoc about a job you work on.")
	return emaillayout.Render(baseURL, c)
}

// renderIssueUpdate is the email for an operator's comment or status change on
// an issue the recipient reported.
func renderIssueUpdate(e Event, baseURL string) (emaillayout.Email, error) {
	c := emaillayout.Content{
		Preheader: BodyPreview(e),
		Heading:   heading(e.Type),
		Body:      emaillayout.P("%s", emaillayout.Strong(e.IssueTitle)),
	}
	if e.IssueStatus != "" {
		c.Body = emaillayout.Join(c.Body, emaillayout.P("The status is now %s.", emaillayout.Strong(e.IssueStatus)))
	}
	if e.Reason != "" {
		c.Body = emaillayout.Join(c.Body, emaillayout.Callout("Comment", e.Reason))
	}
	if e.Link != "" {
		c.Button = &emaillayout.Button{Label: "View the issue", URL: e.Link}
	}
	c.Footer = settingsFooter(baseURL, "Sent by OpenLoc about an issue you reported.")
	return emaillayout.Render(baseURL, c)
}

// settingsFooter is the footer of an email the recipient can mute: why they
// got it, then a link to their notification settings. Empty without the app's
// origin, so the email keeps the default footer.
func settingsFooter(baseURL, why string) template.HTML {
	settings := emaillayout.AppURL(baseURL, "/settings")
	if settings == "" {
		return ""
	}
	return emaillayout.Text("%s %s", why, emaillayout.Link(settings, "Notification settings"))
}

func heading(t EventType) string {
	switch t {
	case EventAssignment:
		return "You've been assigned a new step"
	case EventRejection:
		return "Your step was rejected"
	case EventCompletion:
		return "Workflow completed"
	case EventDeadline:
		return "Deadline approaching"
	case EventIssueUpdated:
		return "Your issue has an update"
	default:
		return ""
	}
}
