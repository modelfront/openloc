package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/ptr"
	"openloc/internal/sliceutil"
)

// Tag limits: free labels such as "mvp" or "blocks-testing".
const (
	maxTags   = 20
	maxTagLen = 40
)

// IssueUpdate is an operator's change to an issue's other fields. A nil field
// stays as it is. Severity "" is none (anything but a bug); with Kind, it is
// the new kind's severity. Priority "" clears it. AssigneeSet with a nil
// AssigneeID unassigns.
type IssueUpdate struct {
	Kind, Severity, Area, Priority *string
	Tags                           *[]string
	AssigneeSet                    bool
	AssigneeID                     *int64
}

// PlatformUpdate applies an operator's change and records each changed field
// in the history: kind, severity, area, priority and tags as internal
// entries, the assignee as an entry the org sees. Only a support team member
// may be assigned. Nothing tells the reporter.
func (s *Service) PlatformUpdate(ctx context.Context, op Operator, id int64, u IssueUpdate) (PlatformIssueDetail, error) {
	if u.Kind != nil && kindLabels[*u.Kind] == "" {
		return PlatformIssueDetail{}, clientError("Pick a kind: Bug, Enhancement or Feature request.")
	}
	if u.Priority != nil && *u.Priority != "" && priorityLabels[*u.Priority] == "" {
		return PlatformIssueDetail{}, clientError("Pick a priority: Low, Medium or High.")
	}
	var tags []string
	if u.Tags != nil {
		var err error
		if tags, err = cleanTags(*u.Tags); err != nil {
			return PlatformIssueDetail{}, err
		}
	}
	if u.AssigneeSet && u.AssigneeID != nil {
		if err := s.checkAssignee(ctx, *u.AssigneeID); err != nil {
			return PlatformIssueDetail{}, err
		}
	}
	_, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.LockIssueForPlatform(ctx, id)
		if err != nil {
			return db.Issue{}, err
		}
		r := recorder{ctx: ctx, q: q, issueID: id, author: &op.UserID}
		if u.Kind != nil || u.Severity != nil || u.Area != nil {
			r.triage(issue, u.Kind, u.Severity, u.Area)
		}
		if u.Priority != nil {
			r.priority(issue, *u.Priority)
		}
		if u.Tags != nil {
			r.tags(issue, tags)
		}
		if u.AssigneeSet {
			r.assignee(issue, u.AssigneeID)
		}
		return issue, r.err
	})
	if err != nil {
		return PlatformIssueDetail{}, err
	}
	logging.Info(ctx, "updated an issue", "issue-updated", logging.IssueID(id))
	return s.PlatformGet(ctx, id)
}

// recorder applies field changes to a locked issue and records each one. The
// first error stops the rest.
type recorder struct {
	ctx     context.Context
	q       *db.Queries
	issueID int64
	author  *int64
	// side records the entries as the support team's (the default) or the
	// reporter's org's.
	side string
	err  error
}

func (r *recorder) record(field string, from, to *string) {
	if r.err == nil {
		side := r.side
		if side == "" {
			side = sideOperator
		}
		r.err = recordChange(r.ctx, r.q, r.issueID, r.author, side, change{field: field, from: from, to: to})
	}
}

// triage changes the kind, severity and area. A new kind without a severity
// drops the severity, or, for a bug, starts it at the report form's default.
func (r *recorder) triage(issue db.Issue, kind, severity, area *string) {
	if r.err != nil {
		return
	}
	newKind, newSeverity, newArea := issue.Kind, ptr.Deref(issue.Severity), issue.Area
	if kind != nil {
		newKind = *kind
	}
	switch {
	case severity != nil:
		newSeverity = *severity
	case newKind != issue.Kind && newKind == KindBug:
		newSeverity = defaultSeverity
	case newKind != issue.Kind:
		newSeverity = ""
	}
	if area != nil {
		newArea = *area
	}
	sev, ar, err := triage(newKind, newSeverity, newArea)
	if err != nil {
		r.err = err
		return
	}
	if newKind == issue.Kind && ptr.Deref(sev) == ptr.Deref(issue.Severity) && ar == issue.Area {
		return
	}
	if _, err := r.q.UpdateIssueTriage(r.ctx, db.UpdateIssueTriageParams{ID: r.issueID, Kind: newKind, Severity: sev, Area: ar}); err != nil {
		r.err = fmt.Errorf("update issue triage: %w", err)
		return
	}
	if newKind != issue.Kind {
		r.record(fieldKind, &issue.Kind, &newKind)
	}
	if ptr.Deref(sev) != ptr.Deref(issue.Severity) {
		r.record(fieldSeverity, issue.Severity, sev)
	}
	if ar != issue.Area {
		r.record(fieldArea, &issue.Area, &ar)
	}
}

func (r *recorder) priority(issue db.Issue, priority string) {
	if r.err != nil || priority == ptr.Deref(issue.Priority) {
		return
	}
	p := ptr.NilIfZero(priority)
	if _, err := r.q.SetIssuePriority(r.ctx, db.SetIssuePriorityParams{ID: r.issueID, Priority: p}); err != nil {
		r.err = fmt.Errorf("set issue priority: %w", err)
		return
	}
	r.record(fieldPriority, issue.Priority, p)
}

func (r *recorder) tags(issue db.Issue, tags []string) {
	if r.err != nil || slices.Equal(tags, issue.Tags) {
		return
	}
	if _, err := r.q.SetIssueTags(r.ctx, db.SetIssueTagsParams{ID: r.issueID, Tags: tags}); err != nil {
		r.err = fmt.Errorf("set issue tags: %w", err)
		return
	}
	r.record(fieldTags, ptr.NilIfZero(strings.Join(issue.Tags, ",")), ptr.NilIfZero(strings.Join(tags, ",")))
}

func (r *recorder) assignee(issue db.Issue, assigneeID *int64) {
	if r.err != nil || ptr.Equal(issue.AssigneeID, assigneeID) {
		return
	}
	if _, err := r.q.SetIssueAssignee(r.ctx, db.SetIssueAssigneeParams{ID: r.issueID, AssigneeID: assigneeID}); err != nil {
		r.err = fmt.Errorf("set issue assignee: %w", err)
		return
	}
	r.record(fieldAssignee, idString(issue.AssigneeID), idString(assigneeID))
}

// cleanTags lower-cases the tags, turns spaces into hyphens, drops empty and
// repeated ones, and sorts them. A tag holds letters, digits, '-', '_' and '.'.
func cleanTags(raw []string) ([]string, error) {
	seen := map[string]bool{}
	tags := []string{}
	for _, t := range raw {
		t = strings.Join(strings.Fields(strings.ToLower(t)), "-")
		if t == "" || seen[t] {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagLen {
			return nil, clientError(fmt.Sprintf("A tag has at most %d characters.", maxTagLen))
		}
		for _, c := range t {
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("-_.", c) {
				return nil, clientError("A tag holds only letters, digits, '-', '_' and '.'.")
			}
		}
		seen[t] = true
		tags = append(tags, t)
	}
	if len(tags) > maxTags {
		return nil, clientError(fmt.Sprintf("An issue has at most %d tags.", maxTags))
	}
	slices.Sort(tags)
	return tags, nil
}

// checkAssignee refuses anyone outside the support team.
func (s *Service) checkAssignee(ctx context.Context, userID int64) error {
	user, err := db.New(s.pool).GetUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !s.opSet.Has(user.Email)) {
		return clientError("Assign the issue to a member of the support team.")
	}
	if err != nil {
		return fmt.Errorf("load assignee: %w", err)
	}
	return nil
}

// PlatformNote adds an internal note: a support team comment, with files,
// that the reporter's org never sees. It changes nothing on the issue and
// tells nobody.
func (s *Service) PlatformNote(ctx context.Context, op Operator, id int64, in NewComment) (PlatformIssueDetail, error) {
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
	_, err = s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.LockIssueForPlatform(ctx, id)
		if err != nil {
			return db.Issue{}, err
		}
		return issue, addNote(ctx, q, id, &op.UserID, body, files)
	})
	if err != nil {
		s.dropFiles(ctx, files)
		return PlatformIssueDetail{}, err
	}
	logging.Info(ctx, "added an internal note to an issue", "issue-note-added", logging.IssueID(id))
	return s.PlatformGet(ctx, id)
}

// addNote records an internal note with its files. Runs inside the caller's
// transaction, after the issue is locked.
func addNote(ctx context.Context, q *db.Queries, issueID int64, authorID *int64, body *string, files []storedFile) error {
	c, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: issueID, AuthorID: authorID, AuthorSide: sideOperator, Body: body, Internal: true,
	})
	if err != nil {
		return fmt.Errorf("create internal note: %w", err)
	}
	return recordCommentFiles(ctx, q, issueID, c.ID, body, files)
}

// Assignable is a support team member an issue may be assigned to.
type Assignable struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// PlatformAssignees is the support team: the accounts of PLATFORM_ADMIN_EMAILS.
func (s *Service) PlatformAssignees(ctx context.Context) ([]Assignable, error) {
	rows, err := db.New(s.pool).ListUsersByEmails(ctx, s.opSet.Emails())
	if err != nil {
		return nil, fmt.Errorf("list support team: %w", err)
	}
	out := make([]Assignable, 0, len(rows))
	for _, r := range rows {
		out = append(out, Assignable{ID: r.ID, Name: r.Name, Email: r.Email})
	}
	return out, nil
}

// PlatformTags is every tag in use, sorted.
func (s *Service) PlatformTags(ctx context.Context) ([]string, error) {
	tags, err := db.New(s.pool).ListIssueTagsForPlatform(ctx)
	if err != nil {
		return nil, fmt.Errorf("list issue tags: %w", err)
	}
	return sliceutil.NonNil(tags), nil
}
