package issues

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// NewComment is a comment's text and files.
type NewComment struct {
	Body  string
	Files []Upload
}

// MemberComment adds a comment from the org's side: the reporter, or a manager
// who sees the issue. It answers a question (needs info → open) and reopens a
// resolved or declined issue.
func (s *Service) MemberComment(ctx context.Context, m Member, id int64, in NewComment) (IssueDetail, error) {
	body, err := commentBody(in)
	if err != nil {
		return IssueDetail{}, err
	}
	recent, err := db.New(s.pool).CountRecentMemberComments(ctx, db.CountRecentMemberCommentsParams{
		AuthorID: &m.UserID, Since: timestamptz(s.now().Add(-rateLimitWindow)),
	})
	if err != nil {
		return IssueDetail{}, fmt.Errorf("count recent comments: %w", err)
	}
	if recent >= commentRateLimit {
		return IssueDetail{}, errTooManyComments
	}
	files, err := s.storeFiles(ctx, m.TenantID, in.Files)
	if err != nil {
		return IssueDetail{}, err
	}
	issue, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.LockMemberIssue(ctx, db.LockMemberIssueParams{
			ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
		})
		if err != nil {
			return db.Issue{}, err
		}
		e := entry{authorID: &m.UserID, side: sideReporter, body: body, files: files}
		if reopenedByReporter[issue.Status] {
			open := StatusOpen
			e.status = &open
		}
		return addEntry(ctx, q, issue.ID, e)
	})
	if err != nil {
		s.dropFiles(ctx, files)
		return IssueDetail{}, err
	}
	logging.Info(ctx, "commented on an issue", "issue-reporter-commented", logging.IssueID(id))
	s.emailOperatorsReply(ctx, issue, body)
	return s.Get(ctx, m, id)
}

// Attachment opens one of the files of an issue the member may see.
func (s *Service) Attachment(ctx context.Context, m Member, id, attachmentID int64) (Attachment, error) {
	if _, err := db.New(s.pool).GetMemberIssue(ctx, db.GetMemberIssueParams{
		ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
	}); err != nil {
		return Attachment{}, err
	}
	f, err := db.New(s.pool).GetMemberIssueAttachment(ctx, db.GetMemberIssueAttachmentParams{ID: attachmentID, IssueID: id})
	if err != nil {
		return Attachment{}, err
	}
	return s.openAttachment(ctx, f)
}

// Confirm records that the reporter's org checked the fix of a resolved issue.
func (s *Service) Confirm(ctx context.Context, m Member, id int64) (IssueDetail, error) {
	_, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.LockMemberIssue(ctx, db.LockMemberIssueParams{
			ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
		})
		if err != nil {
			return db.Issue{}, err
		}
		if issue.Status != StatusResolved {
			return db.Issue{}, clientError("Only a resolved issue can be confirmed as fixed.")
		}
		if issue.ConfirmedAt.Valid {
			return db.Issue{}, clientError("The fix is already confirmed.")
		}
		confirmed, err := q.ConfirmIssueFixed(ctx, id)
		if err != nil {
			return db.Issue{}, fmt.Errorf("confirm issue: %w", err)
		}
		return confirmed, recordChange(ctx, q, id, &m.UserID, sideReporter, change{field: fieldConfirmed})
	})
	if err != nil {
		return IssueDetail{}, err
	}
	logging.Info(ctx, "confirmed an issue's fix", "issue-fix-confirmed", logging.IssueID(id))
	return s.Get(ctx, m, id)
}

// entry is a visible entry of the conversation: a comment, a status change,
// or both, with its files. A status change to duplicate names the original.
type entry struct {
	authorID     *int64
	side         string
	body, status *string
	duplicateOf  *int64
	files        []storedFile
}

// addEntry records a visible entry and updates the issue's activity: who acted
// last, the status, the original of a duplicate. Runs inside the caller's
// transaction, after the issue is locked.
func addEntry(ctx context.Context, q *db.Queries, issueID int64, e entry) (db.Issue, error) {
	p := db.CreateIssueCommentParams{IssueID: issueID, AuthorID: e.authorID, AuthorSide: e.side, Body: e.body, Status: e.status}
	if e.duplicateOf != nil {
		field := fieldDuplicate
		p.Field, p.ToValue = &field, idString(e.duplicateOf)
	}
	c, err := q.CreateIssueComment(ctx, p)
	if err != nil {
		return db.Issue{}, fmt.Errorf("create issue comment: %w", err)
	}
	if err := recordCommentFiles(ctx, q, issueID, c.ID, e.body, e.files); err != nil {
		return db.Issue{}, err
	}
	issue, err := q.UpdateIssueActivity(ctx, db.UpdateIssueActivityParams{
		ID: issueID, Status: e.status, DuplicateOfID: e.duplicateOf, LastActivityBy: &e.side,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("update issue activity: %w", err)
	}
	return issue, nil
}

// commentBody is the comment's text, or nil when files alone are sent.
func commentBody(in NewComment) (*string, error) {
	if err := checkUploads(in.Files); err != nil {
		return nil, err
	}
	if len(in.Files) > 0 && in.Body == "" {
		return nil, nil
	}
	body, err := cleanText(in.Body, "A comment", 1, maxText)
	if err != nil {
		return nil, err
	}
	if err := checkUploadRefs(body, in.Files); err != nil {
		return nil, err
	}
	return &body, nil
}
