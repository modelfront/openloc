package issues

import (
	"context"
	"fmt"
	"strconv"

	db "openloc/internal/database/sqlcgen"
)

// Fields an entry changes besides the status. Priority, severity, area and
// tag changes, and the support team's kind changes, are internal (a table
// check holds it); the others reach the reporter's org, the kind changes its
// own side makes among them.
const (
	fieldKind      = "kind"
	fieldAssignee  = "assignee"
	fieldPriority  = "priority"
	fieldSeverity  = "severity"
	fieldArea      = "area"
	fieldTags      = "tags"
	fieldDuplicate = "duplicate"
	fieldConfirmed = "confirmed"
)

// formerMember names a deleted account in the history; unnamedMember an
// account without a name.
const (
	formerMember  = "Former member"
	unnamedMember = "Support team member"
)

// commentRow is the shape both comment queries return.
type commentRow db.ListIssueCommentsForPlatformRow

// change is a field change an entry records: values as stored (an assignee
// is a user id).
type change struct {
	field    string
	from, to *string
}

// recordChange adds an entry for a field change, with no comment. Runs inside
// the caller's transaction, after the issue is locked; it does not touch the
// issue row.
func recordChange(ctx context.Context, q *db.Queries, issueID int64, authorID *int64, side string, c change) error {
	_, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: issueID, AuthorID: authorID, AuthorSide: side, Internal: internalField(c.field, side),
		Field: &c.field, FromValue: c.from, ToValue: c.to,
	})
	if err != nil {
		return fmt.Errorf("record issue %s change: %w", c.field, err)
	}
	return nil
}

func internalField(field, side string) bool {
	switch field {
	case fieldPriority, fieldSeverity, fieldArea, fieldTags:
		return true
	case fieldKind:
		return side == sideOperator
	}
	return false
}

// thread is an issue's own files and its conversation, as one side sees them.
type thread struct {
	files    []AttachmentView
	comments []CommentView
}

// memberThread is what a member of the org sees: no internal entry, no file
// of one, and no original's id on a duplicate entry (the issue itself carries
// the original when the member may open it).
func (s *Service) memberThread(ctx context.Context, issueID int64, rows []commentRow) (thread, error) {
	files, err := db.New(s.pool).ListMemberIssueAttachments(ctx, issueID)
	if err != nil {
		return thread{}, fmt.Errorf("list issue attachments: %w", err)
	}
	return s.assemble(ctx, rows, files, true)
}

// platformThread is what the support team sees: every entry and file.
func (s *Service) platformThread(ctx context.Context, issueID int64, rows []commentRow) (thread, error) {
	files, err := db.New(s.pool).ListIssueAttachments(ctx, issueID)
	if err != nil {
		return thread{}, fmt.Errorf("list issue attachments: %w", err)
	}
	return s.assemble(ctx, rows, files, false)
}

// assemble builds the conversation: each file goes on the comment it was sent
// with, or on the issue itself. A file whose comment is not in rows is left out.
func (s *Service) assemble(ctx context.Context, rows []commentRow, files []db.IssueAttachment, forMember bool) (thread, error) {
	names, err := s.assigneeNames(ctx, rows)
	if err != nil {
		return thread{}, err
	}
	t := thread{files: []AttachmentView{}, comments: make([]CommentView, 0, len(rows))}
	byComment := map[int64]int{}
	for i, c := range rows {
		t.comments = append(t.comments, CommentView{
			ID: c.ID, AuthorID: c.AuthorID, AuthorSide: c.AuthorSide, AuthorName: c.AuthorName, Body: c.Body,
			Status: c.Status, Internal: c.Internal, Change: changeView(c, names, forMember),
			Attachments: []AttachmentView{}, CreatedAt: c.CreatedAt.Time,
		})
		byComment[c.ID] = i
	}
	for _, f := range files {
		v := attachmentView(f)
		if f.CommentID == nil {
			t.files = append(t.files, v)
			continue
		}
		if i, ok := byComment[*f.CommentID]; ok {
			t.comments[i].Attachments = append(t.comments[i].Attachments, v)
		}
	}
	return t, nil
}

func changeView(c commentRow, names map[int64]string, forMember bool) *ChangeView {
	if c.Field == nil {
		return nil
	}
	v := &ChangeView{Field: *c.Field, From: c.FromValue, To: c.ToValue}
	switch *c.Field {
	case fieldAssignee:
		v.From, v.To = assigneeName(c.FromValue, names), assigneeName(c.ToValue, names)
	case fieldDuplicate:
		v.From = nil
		if forMember {
			v.To = nil
		}
	case fieldConfirmed:
		v.From, v.To = nil, nil
	}
	return v
}

// assigneeName turns a stored user id into the name the history shows.
func assigneeName(id *string, names map[int64]string) *string {
	if id == nil {
		return nil
	}
	n, err := strconv.ParseInt(*id, 10, 64)
	name, ok := names[n]
	switch {
	case err != nil || !ok:
		name = formerMember
	case name == "":
		name = unnamedMember
	}
	return &name
}

// assigneeNames loads the names of the users the assignee entries name.
func (s *Service) assigneeNames(ctx context.Context, rows []commentRow) (map[int64]string, error) {
	var ids []int64
	for _, c := range rows {
		if c.Field == nil || *c.Field != fieldAssignee {
			continue
		}
		for _, v := range []*string{c.FromValue, c.ToValue} {
			if v == nil {
				continue
			}
			if id, err := strconv.ParseInt(*v, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	names := map[int64]string{}
	if len(ids) == 0 {
		return names, nil
	}
	rowsByID, err := db.New(s.pool).ListUserNames(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load assignee names: %w", err)
	}
	for _, r := range rowsByID {
		names[r.ID] = r.Name
	}
	return names, nil
}

// idString is a user or issue id as an entry stores it.
func idString(id *int64) *string {
	if id == nil {
		return nil
	}
	s := strconv.FormatInt(*id, 10)
	return &s
}
