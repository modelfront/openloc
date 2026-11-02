package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"openloc/internal/auth"
	"openloc/internal/issues"
)

// The support tools act as the app's support queue does, through the same
// operator methods of the issue service as /api/platform/issues: every
// organization's issues, the internal notes and the team's own fields
// included. The caller is a platform operator; the server checks that before
// any of these runs (requireOperator).

func (b *directBackend) IsOperator(ctx context.Context) (bool, error) {
	if b.issues == nil || !b.issues.Enabled() {
		return false, nil
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false, nil
	}
	return b.issues.IsOperator(ctx, claims.UserID)
}

// supportOperator is the issue service and the caller as the support team
// member acting.
func (b *directBackend) supportOperator(ctx context.Context) (*issues.Service, issues.Operator, error) {
	svc, err := b.issueService()
	if err != nil {
		return nil, issues.Operator{}, err
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, issues.Operator{}, errDenied
	}
	return svc, issues.Operator{UserID: claims.UserID}, nil
}

func (b *directBackend) SupportListIssues(ctx context.Context, in SupportListIssuesIn) (SupportIssueList, error) {
	svc, _, err := b.supportOperator(ctx)
	if err != nil {
		return SupportIssueList{}, err
	}
	sort, err := issues.ParseSort(in.Sort, true)
	if err != nil {
		return SupportIssueList{}, serviceErr(err, "list issues")
	}
	f := issues.Filter{Statuses: in.Statuses, Kinds: in.Kinds, Areas: in.Areas, Severities: in.Severities,
		Search: in.Query, TenantIDs: in.OrgIDs, ReporterIDs: in.ReporterIDs, Priorities: in.Priorities,
		Tags: in.Tags, Verifications: in.Verifications, AssigneeIDs: in.AssigneeIDs, Unassigned: in.Unassigned,
		Sort: sort}
	page, next, err := svc.PlatformList(ctx, f, in.Cursor, int32(min(clampLimit(in.Limit), maxIssuePage)))
	if err != nil {
		return SupportIssueList{}, serviceErr(err, "list issues")
	}
	total, err := svc.PlatformCount(ctx, f)
	if err != nil {
		return SupportIssueList{}, serviceErr(err, "count issues")
	}
	out := SupportIssueList{Issues: make([]SupportIssueSummary, 0, len(page)), Count: len(page), Total: total,
		NextCursor: next, HasMore: next != ""}
	for _, v := range page {
		out.Issues = append(out.Issues, supportIssueSummary(v))
	}
	return out, nil
}

// supportThread runs one operator call of the issue service and returns the
// issue's thread; what names the call in an error.
func (b *directBackend) supportThread(ctx context.Context, what string,
	call func(svc *issues.Service, op issues.Operator) (issues.PlatformIssueDetail, error)) (SupportIssueThread, error) {
	svc, op, err := b.supportOperator(ctx)
	if err != nil {
		return SupportIssueThread{}, err
	}
	d, err := call(svc, op)
	if err != nil {
		return SupportIssueThread{}, serviceErr(err, what)
	}
	return supportIssueThread(d)
}

func (b *directBackend) SupportGetIssue(ctx context.Context, in SupportIssueIDIn) (SupportIssueThread, error) {
	return b.supportThread(ctx, "get issue", func(svc *issues.Service, _ issues.Operator) (issues.PlatformIssueDetail, error) {
		return svc.PlatformGet(ctx, in.IssueID)
	})
}

func (b *directBackend) SupportSetIssueStatus(ctx context.Context, in SupportSetIssueStatusIn) (SupportIssueThread, error) {
	return b.supportThread(ctx, "set issue status", func(svc *issues.Service, op issues.Operator) (issues.PlatformIssueDetail, error) {
		return svc.PlatformSetStatus(ctx, op, in.IssueID, issues.StatusChange{
			Status: in.Status, Note: strings.TrimSpace(in.Note), DuplicateOf: in.DuplicateOf,
		})
	})
}

func (b *directBackend) SupportReplyToIssue(ctx context.Context, in SupportIssueTextIn) (SupportIssueThread, error) {
	return b.supportThread(ctx, "reply to issue", func(svc *issues.Service, op issues.Operator) (issues.PlatformIssueDetail, error) {
		return svc.PlatformComment(ctx, op, in.IssueID, issues.NewComment{Body: in.Text})
	})
}

func (b *directBackend) SupportAddIssueNote(ctx context.Context, in SupportIssueTextIn) (SupportIssueThread, error) {
	return b.supportThread(ctx, "add issue note", func(svc *issues.Service, op issues.Operator) (issues.PlatformIssueDetail, error) {
		return svc.PlatformNote(ctx, op, in.IssueID, issues.NewComment{Body: in.Text})
	})
}

func (b *directBackend) SupportUpdateIssue(ctx context.Context, in SupportUpdateIssueIn) (SupportIssueThread, error) {
	return b.supportThread(ctx, "update issue", func(svc *issues.Service, op issues.Operator) (issues.PlatformIssueDetail, error) {
		u, err := b.issueUpdate(ctx, svc, in)
		if err != nil {
			return issues.PlatformIssueDetail{}, err
		}
		return svc.PlatformUpdate(ctx, op, in.IssueID, u)
	})
}

// issueUpdate is the service's update for the arguments: a field left empty
// stays as it is. A kind without a severity leaves the severity to the
// service's rule (Major for a new bug, none for anything else).
func (b *directBackend) issueUpdate(ctx context.Context, svc *issues.Service, in SupportUpdateIssueIn) (issues.IssueUpdate, error) {
	var u issues.IssueUpdate
	changed := false
	set := func(v string) *string {
		if v == "" {
			return nil
		}
		changed = true
		return &v
	}
	u.Kind, u.Severity, u.Area, u.Priority = set(in.Kind), set(in.Severity), set(in.Area), set(in.Priority)
	if in.ClearPriority {
		if u.Priority != nil {
			return u, invalidf("give a priority or clear_priority, not both")
		}
		empty := ""
		u.Priority, changed = &empty, true
	}
	if in.Tags != nil {
		tags := in.Tags
		u.Tags, changed = &tags, true
	}
	if in.AssigneeEmail != "" && in.Unassign {
		return u, invalidf("give an assignee_email or unassign, not both")
	}
	if in.Unassign {
		u.AssigneeSet, changed = true, true
	}
	if in.AssigneeEmail != "" {
		id, err := b.supportMemberID(ctx, svc, in.AssigneeEmail)
		if err != nil {
			return u, err
		}
		u.AssigneeSet, u.AssigneeID, changed = true, &id, true
	}
	if !changed {
		return u, invalidf("nothing to change: give at least one field")
	}
	return u, nil
}

// supportMemberID is the account of the support team member with this email.
func (b *directBackend) supportMemberID(ctx context.Context, svc *issues.Service, email string) (int64, error) {
	team, err := svc.PlatformAssignees(ctx)
	if err != nil {
		return 0, serviceErr(err, "list support team")
	}
	for _, m := range team {
		if strings.EqualFold(strings.TrimSpace(m.Email), strings.TrimSpace(email)) {
			return m.ID, nil
		}
	}
	return 0, invalidf("%s is not on the support team; openloc_support_list_team lists who is", email)
}

func (b *directBackend) SupportListTeam(ctx context.Context, _ SupportListTeamIn) (SupportTeam, error) {
	svc, _, err := b.supportOperator(ctx)
	if err != nil {
		return SupportTeam{}, err
	}
	team, err := svc.PlatformAssignees(ctx)
	if err != nil {
		return SupportTeam{}, serviceErr(err, "list support team")
	}
	out := SupportTeam{Members: make([]SupportTeamMember, 0, len(team))}
	for _, m := range team {
		out.Members = append(out.Members, SupportTeamMember{ID: m.ID, Name: m.Name, Email: m.Email})
	}
	return out, nil
}

func supportIssueSummary(v issues.PlatformIssueView) SupportIssueSummary {
	s := SupportIssueSummary{IssueSummary: issueSummary(v.IssueView), OrgID: v.TenantID, OrgName: v.OrgName,
		ReporterID: v.ReporterID, AssigneeID: v.AssigneeID, Priority: v.Priority, Tags: v.Tags,
		Verification: v.ChatVerification, VerificationNote: v.ChatVerificationNote, ImportKey: v.ImportKey}
	s.Path = fmt.Sprintf("/platform/issues/%d", v.ID)
	return s
}

func supportIssueThread(d issues.PlatformIssueDetail) (SupportIssueThread, error) {
	t := SupportIssueThread{SupportIssueSummary: supportIssueSummary(d.Issue), Description: d.Issue.Description,
		Files: issueFiles(d.Issue.Attachments), Entries: issueEntries(d.Comments),
		Duplicates: make([]IssueLink, 0, len(d.Issue.Duplicates)),
		HasTrail:   len(d.Trail) > 0, HasRecording: len(d.Recording) > 0}
	if len(d.Issue.Context) > 0 {
		if err := json.Unmarshal(d.Issue.Context, &t.Context); err != nil {
			return SupportIssueThread{}, fmt.Errorf("issue context: %w", err)
		}
	}
	for _, r := range d.Issue.Duplicates {
		t.Duplicates = append(t.Duplicates, IssueLink{ID: r.ID, Title: r.Title, Status: r.Status, CanOpen: r.CanOpen})
	}
	return t, nil
}
