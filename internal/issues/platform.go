package issues

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/ptr"
	"openloc/internal/sliceutil"
)

// Operator is the platform admin acting on an issue.
type Operator struct {
	UserID int64
}

// maxSearch bounds the search text of a list.
const maxSearch = 200

// Filter narrows an issue list and orders it. An empty list matches any value;
// a severity matches bugs only. Search matches a part of the title or the
// description. TenantIDs (the org filter), Priorities, Tags (any of them) and
// the assignee filter (AssigneeIDs, plus the unassigned issues when
// Unassigned) are for operators only. The list is by last update, newest
// first unless OldestFirst.
type Filter struct {
	Statuses, Kinds, Areas, Severities []string
	Search                             string
	TenantIDs                          []int64
	Priorities, Tags                   []string
	Verifications                      []string
	AssigneeIDs                        []int64
	Unassigned                         bool
	OldestFirst                        bool
}

// check refuses values that no issue can have.
func (f Filter) check() error {
	for _, c := range []struct {
		values []string
		known  map[string]string
		what   string
	}{
		{f.Statuses, statusLabels, "status"},
		{f.Kinds, kindLabels, "kind"},
		{f.Areas, areaLabels, "area"},
		{f.Severities, severityLabels, "severity"},
		{f.Priorities, priorityLabels, "priority"},
		{f.Verifications, verificationLabels, "verification"},
	} {
		for _, v := range c.values {
			if c.known[v] == "" {
				return clientError("Unknown " + c.what + ": " + v)
			}
		}
	}
	if utf8.RuneCountInString(f.Search) > maxSearch {
		return clientError(fmt.Sprintf("Search for at most %d characters.", maxSearch))
	}
	return nil
}

// likePattern is the search text for an ILIKE '%…%' match, with the
// wildcards escaped; nil for no search.
func likePattern(search string) *string {
	search = strings.TrimSpace(search)
	if search == "" {
		return nil
	}
	p := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
	return &p
}

// PlatformList is every org's issues, filtered and ordered by f, from cursor on.
func (s *Service) PlatformList(ctx context.Context, f Filter, cursor Cursor, limit int32) ([]PlatformIssueView, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	p := db.ListIssuesForPlatformNewestParams{
		Statuses: sliceutil.NonNil(f.Statuses), Kinds: sliceutil.NonNil(f.Kinds), Areas: sliceutil.NonNil(f.Areas),
		Severities: sliceutil.NonNil(f.Severities), TenantIds: sliceutil.NonNil(f.TenantIDs),
		Priorities: sliceutil.NonNil(f.Priorities), Tags: sliceutil.NonNil(f.Tags),
		Verifications: sliceutil.NonNil(f.Verifications), AssigneeIds: sliceutil.NonNil(f.AssigneeIDs),
		Unassigned: f.Unassigned, Search: likePattern(f.Search),
		CursorAt: cursor.at(), CursorID: cursor.ID, RowLimit: limit,
	}
	q := db.New(s.pool)
	var rows []db.ListIssuesForPlatformNewestRow
	var err error
	if f.OldestFirst {
		var old []db.ListIssuesForPlatformOldestRow
		old, err = q.ListIssuesForPlatformOldest(ctx, db.ListIssuesForPlatformOldestParams(p))
		for _, r := range old {
			rows = append(rows, db.ListIssuesForPlatformNewestRow(r))
		}
	} else {
		rows, err = q.ListIssuesForPlatformNewest(ctx, p)
	}
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	out := make([]PlatformIssueView, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueRow(r).platformView())
	}
	return out, nil
}

// IssueOrg is an org that has issues, for the operators' organization filter.
type IssueOrg struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// PlatformOrgs is every org that has issues, by name.
func (s *Service) PlatformOrgs(ctx context.Context) ([]IssueOrg, error) {
	rows, err := db.New(s.pool).ListIssueOrgsForPlatform(ctx)
	if err != nil {
		return nil, fmt.Errorf("list issue orgs: %w", err)
	}
	out := make([]IssueOrg, 0, len(rows))
	for _, r := range rows {
		out = append(out, IssueOrg{ID: r.ID, Name: r.Name})
	}
	return out, nil
}

// PlatformDelete soft-deletes any org's issue.
func (s *Service) PlatformDelete(ctx context.Context, id int64) error {
	err := s.softDelete(ctx, id, func(q *db.Queries) (int64, error) {
		return q.SoftDeleteIssueForPlatform(ctx, id)
	})
	if err != nil {
		return err
	}
	logging.Info(ctx, "deleted an issue as operator", "issue-operator-deleted", logging.IssueID(id))
	return nil
}

// PlatformGet is any org's issue with its whole conversation, internal entries
// included, and its files.
func (s *Service) PlatformGet(ctx context.Context, id int64) (PlatformIssueDetail, error) {
	q := db.New(s.pool)
	row, err := q.GetIssueForPlatform(ctx, id)
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	rows, err := q.ListIssueCommentsForPlatform(ctx, id)
	if err != nil {
		return PlatformIssueDetail{}, fmt.Errorf("list issue comments: %w", err)
	}
	comments := make([]commentRow, 0, len(rows))
	for _, r := range rows {
		comments = append(comments, commentRow(r))
	}
	t, err := s.platformThread(ctx, id, comments)
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	capture, err := q.GetIssueCaptureForPlatform(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PlatformIssueDetail{}, fmt.Errorf("get issue capture: %w", err)
	}
	dups, err := q.ListIssueDuplicatesForPlatform(ctx, id)
	if err != nil {
		return PlatformIssueDetail{}, fmt.Errorf("list issue duplicates: %w", err)
	}
	view := issueRow(row).platformView()
	view.Attachments = t.files
	view.Duplicates = make([]IssueRef, 0, len(dups))
	for _, d := range dups {
		view.Duplicates = append(view.Duplicates, IssueRef{ID: d.ID, Title: d.Title, Status: d.Status, CanOpen: true})
	}
	return PlatformIssueDetail{Issue: view, Comments: t.comments, Trail: capture.Trail, Recording: capture.Recording}, nil
}

// StatusChange is an operator's move: a status, an optional comment for the
// reporter, and for a duplicate the original issue.
type StatusChange struct {
	Status, Note string
	DuplicateOf  *int64
}

// PlatformSetStatus changes an issue's status, with an optional comment, and
// tells the reporter; a resolve also rings the bell of the org's managers.
// Setting the current status again only records the comment, except a
// duplicate pointed at another original.
func (s *Service) PlatformSetStatus(ctx context.Context, op Operator, id int64, in StatusChange) (PlatformIssueDetail, error) {
	if statusLabels[in.Status] == "" {
		return PlatformIssueDetail{}, clientError("Unknown status: " + in.Status)
	}
	duplicate := in.Status == StatusDuplicate
	if duplicate && in.DuplicateOf == nil {
		return PlatformIssueDetail{}, clientError("Enter the number of the original issue.")
	}
	if !duplicate && in.DuplicateOf != nil {
		return PlatformIssueDetail{}, clientError("Only a duplicate names an original issue.")
	}
	var body *string
	if in.Note != "" {
		b, err := cleanText(in.Note, "A comment", 1, maxText)
		if err != nil {
			return PlatformIssueDetail{}, err
		}
		body = &b
	}
	var changed *string
	issue, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		// Taken before the row lock: the request that holds it may need to
		// move this very row.
		if duplicate {
			if err := q.LockIssueDuplicates(ctx); err != nil {
				return db.Issue{}, fmt.Errorf("lock duplicates: %w", err)
			}
		}
		issue, err := q.LockIssueForPlatform(ctx, id)
		if err != nil {
			return db.Issue{}, err
		}
		if duplicate {
			if err := checkOriginal(ctx, q, id, *in.DuplicateOf); err != nil {
				return db.Issue{}, err
			}
		}
		if issue.Status != in.Status || (duplicate && !ptr.Equal(issue.DuplicateOfID, in.DuplicateOf)) {
			changed = &in.Status
		}
		if changed == nil && body == nil {
			return db.Issue{}, clientError("The issue already has this status.")
		}
		if changed != nil && *changed == StatusNeedsInfo && body == nil {
			return db.Issue{}, clientError("Write the question the reporter should answer.")
		}
		e := entry{authorID: &op.UserID, side: sideOperator, body: body, status: changed}
		if changed != nil && duplicate {
			e.duplicateOf = in.DuplicateOf
		}
		updated, err := addEntry(ctx, q, issue.ID, e)
		if err != nil || e.duplicateOf == nil {
			return updated, err
		}
		return updated, repointDuplicates(ctx, q, issue.ID, *e.duplicateOf, op)
	})
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	logging.Info(ctx, "changed an issue's status", "issue-status-changed", logging.IssueID(id))
	s.notifyReporter(ctx, issue, op, changed, body)
	if changed != nil && *changed == StatusResolved {
		s.notifyOrgResolved(ctx, issue, op)
	}
	return s.PlatformGet(ctx, id)
}

// repointDuplicates moves the duplicates of an issue that just became a
// duplicate onto its original, each with an entry its org sees.
func repointDuplicates(ctx context.Context, q *db.Queries, id, originalID int64, op Operator) error {
	moved, err := q.RepointDuplicates(ctx, db.RepointDuplicatesParams{ID: id, OriginalID: originalID})
	if err != nil {
		return fmt.Errorf("repoint duplicates: %w", err)
	}
	for _, dupID := range moved {
		if err := recordChange(ctx, q, dupID, &op.UserID, sideOperator,
			change{field: fieldDuplicate, to: idString(&originalID)}); err != nil {
			return err
		}
	}
	return nil
}

// checkOriginal refuses an original that is missing, the issue itself, or
// another duplicate. The original may belong to any org.
func checkOriginal(ctx context.Context, q *db.Queries, id, originalID int64) error {
	if originalID == id {
		return clientError("An issue cannot be a duplicate of itself.")
	}
	original, err := q.GetIssueForPlatform(ctx, originalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return clientError(fmt.Sprintf("There is no issue #%d.", originalID))
	}
	if err != nil {
		return fmt.Errorf("load original issue: %w", err)
	}
	if original.Status == StatusDuplicate {
		return clientError(fmt.Sprintf("Issue #%d is itself a duplicate. Pick its original.", originalID))
	}
	return nil
}

// PlatformComment adds an operator's comment and tells the reporter. It never
// changes the status; asking the reporter something is PlatformSetStatus with
// needs_info and the question as its comment.
func (s *Service) PlatformComment(ctx context.Context, op Operator, id int64, in NewComment) (PlatformIssueDetail, error) {
	body, err := commentBody(in)
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	row, err := db.New(s.pool).GetIssueForPlatform(ctx, id)
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	files, err := s.storeFiles(ctx, row.TenantID, in.Files)
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	updated, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		if _, err := q.LockIssueForPlatform(ctx, id); err != nil {
			return db.Issue{}, err
		}
		return addEntry(ctx, q, id, entry{authorID: &op.UserID, side: sideOperator, body: body, files: files})
	})
	if err != nil {
		s.dropFiles(ctx, files)
		return PlatformIssueDetail{}, err
	}
	logging.Info(ctx, "commented on an issue", "issue-operator-commented", logging.IssueID(id))
	s.notifyReporter(ctx, updated, op, nil, body)
	return s.PlatformGet(ctx, id)
}

// PlatformAttachment opens one of the files of any org's issue, internal
// notes' files included.
func (s *Service) PlatformAttachment(ctx context.Context, id, attachmentID int64) (Attachment, error) {
	q := db.New(s.pool)
	if _, err := q.GetIssueForPlatform(ctx, id); err != nil {
		return Attachment{}, err
	}
	f, err := q.GetIssueAttachment(ctx, db.GetIssueAttachmentParams{ID: attachmentID, IssueID: id})
	if err != nil {
		return Attachment{}, err
	}
	return s.openAttachment(ctx, f)
}
