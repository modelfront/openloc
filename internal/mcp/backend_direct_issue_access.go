package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"openloc/internal/issues"
)

// The issue tools act as the app's Reported issues page does, through the
// same member methods of the issue service: they see and act on what the
// caller sees there (an owner, admin or project manager the organization's
// issues, anyone else their own), and never the support team's side.

// maxIssuePage is the app's own page cap for the issue lists.
const maxIssuePage = 100

func (b *directBackend) ListIssues(ctx context.Context, in ListIssuesIn) (IssueList, error) {
	svc, m, err := b.issueMember(ctx)
	if err != nil {
		return IssueList{}, err
	}
	sort, err := issues.ParseSort(in.Sort, false)
	if err != nil {
		return IssueList{}, serviceErr(err, "list issues")
	}
	f := issues.Filter{Statuses: in.Statuses, Kinds: in.Kinds, Areas: in.Areas, Severities: in.Severities,
		Search: in.Query, Sort: sort}
	page, next, err := svc.List(ctx, m, f, in.Cursor, int32(min(clampLimit(in.Limit), maxIssuePage)))
	if err != nil {
		return IssueList{}, serviceErr(err, "list issues")
	}
	total, err := svc.Count(ctx, m, f)
	if err != nil {
		return IssueList{}, serviceErr(err, "count issues")
	}
	out := IssueList{Issues: make([]IssueSummary, 0, len(page)), Count: len(page), Total: total,
		NextCursor: next, HasMore: next != ""}
	for _, v := range page {
		out.Issues = append(out.Issues, issueSummary(v))
	}
	return out, nil
}

func (b *directBackend) GetIssue(ctx context.Context, in IssueIDIn) (IssueThread, error) {
	svc, m, err := b.issueMember(ctx)
	if err != nil {
		return IssueThread{}, err
	}
	d, err := svc.Get(ctx, m, in.IssueID)
	if err != nil {
		return IssueThread{}, serviceErr(err, "get issue")
	}
	return issueThread(d)
}

func (b *directBackend) ReplyToIssue(ctx context.Context, in ReplyToIssueIn) (IssueThread, error) {
	svc, m, err := b.issueMember(ctx)
	if err != nil {
		return IssueThread{}, err
	}
	d, err := svc.MemberComment(ctx, m, in.IssueID, issues.NewComment{Body: in.Text})
	if err != nil {
		return IssueThread{}, serviceErr(err, "reply to issue")
	}
	return issueThread(d)
}

func (b *directBackend) ConfirmIssueFix(ctx context.Context, in IssueIDIn) (IssueThread, error) {
	svc, m, err := b.issueMember(ctx)
	if err != nil {
		return IssueThread{}, err
	}
	d, err := svc.Confirm(ctx, m, in.IssueID)
	if err != nil {
		return IssueThread{}, serviceErr(err, "confirm issue fix")
	}
	return issueThread(d)
}

func (b *directBackend) ChangeIssueKind(ctx context.Context, in ChangeIssueKindIn) (IssueThread, error) {
	svc, m, err := b.issueMember(ctx)
	if err != nil {
		return IssueThread{}, err
	}
	d, err := svc.ChangeKind(ctx, m, in.IssueID, issues.KindChange{Kind: in.Kind, Severity: in.Severity})
	if err != nil {
		return IssueThread{}, serviceErr(err, "change issue kind")
	}
	return issueThread(d)
}

// issueMember is the issue service and the caller as it knows them.
func (b *directBackend) issueMember(ctx context.Context) (*issues.Service, issues.Member, error) {
	svc, err := b.issueService()
	if err != nil {
		return nil, issues.Member{}, err
	}
	m, err := member(ctx)
	return svc, m, err
}

func issueSummary(v issues.IssueView) IssueSummary {
	s := IssueSummary{
		ID: v.ID, Kind: v.Kind, Severity: v.Severity, Area: v.Area, Title: v.Title, Status: v.Status,
		LastActivityBy: v.LastActivityBy, CreatedAt: timeString(v.CreatedAt), UpdatedAt: timeString(v.UpdatedAt),
		ResolvedAt: optRFC3339(v.ResolvedAt), ConfirmedAt: optRFC3339(v.ConfirmedAt),
		ReporterName: v.ReporterName, ReporterEmail: v.ReporterEmail, Path: fmt.Sprintf("/issues/%d", v.ID),
	}
	if v.Assignee != nil {
		s.Assignee = v.Assignee.Name
		if s.Assignee == "" {
			s.Assignee = "the support team"
		}
	}
	if d := v.DuplicateOf; d != nil {
		s.DuplicateOf = &IssueLink{ID: d.ID, Title: d.Title, Status: d.Status, CanOpen: d.CanOpen}
	}
	return s
}

func issueThread(d issues.IssueDetail) (IssueThread, error) {
	t := IssueThread{IssueSummary: issueSummary(d.Issue), Description: d.Issue.Description,
		Files: issueFiles(d.Issue.Attachments), Entries: issueEntries(d.Comments)}
	if len(d.Issue.Context) > 0 {
		if err := json.Unmarshal(d.Issue.Context, &t.Context); err != nil {
			return IssueThread{}, fmt.Errorf("issue context: %w", err)
		}
	}
	return t, nil
}

// issueEntries is a conversation as the tools give it. A member's
// conversation holds no internal entry; the support team's marks them.
func issueEntries(comments []issues.CommentView) []IssueEntry {
	out := make([]IssueEntry, 0, len(comments))
	for _, c := range comments {
		e := IssueEntry{AuthorSide: c.AuthorSide, AuthorName: c.AuthorName, Text: c.Body, Status: c.Status,
			Internal: c.Internal, Files: issueFiles(c.Attachments), CreatedAt: timeString(c.CreatedAt)}
		if c.Change != nil {
			e.Change = &IssueChange{Field: c.Change.Field, From: c.Change.From, To: c.Change.To}
		}
		out = append(out, e)
	}
	return out
}

func issueFiles(files []issues.AttachmentView) []IssueFile {
	out := make([]IssueFile, 0, len(files))
	for _, f := range files {
		out = append(out, IssueFile{Filename: f.Filename, SizeBytes: f.SizeBytes, IsImage: f.IsImage})
	}
	return out
}

func optRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timeString(*t)
	return &s
}
