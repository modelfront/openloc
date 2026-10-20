package issues

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
)

// KindChange is a new kind for an issue. Severity is optional: a bug without
// one starts at the report form's default, and any other kind has none.
type KindChange struct {
	Kind, Severity string
}

// ChangeKind changes the kind of an issue the member may see, as commenting
// does: the reporter, or a manager who sees the issue. It runs the support
// team's triage, recorded from the reporter's side: the kind change is an
// entry the org sees, the severity it sets or clears an internal one.
// Nobody is notified.
func (s *Service) ChangeKind(ctx context.Context, m Member, id int64, in KindChange) (IssueDetail, error) {
	if kindLabels[in.Kind] == "" {
		return IssueDetail{}, clientError("Pick a kind: Bug, Enhancement or Feature request.")
	}
	_, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.LockMemberIssue(ctx, db.LockMemberIssueParams{
			ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
		})
		if err != nil {
			return db.Issue{}, err
		}
		if issue.Kind == in.Kind {
			return db.Issue{}, clientError("The issue already has this kind.")
		}
		var severity *string
		if in.Severity != "" {
			severity = &in.Severity
		}
		r := recorder{ctx: ctx, q: q, issueID: id, author: &m.UserID, side: sideReporter}
		r.triage(issue, &in.Kind, severity, nil)
		return issue, r.err
	})
	if err != nil {
		return IssueDetail{}, err
	}
	logging.Info(ctx, "changed an issue's kind", "issue-kind-changed", logging.IssueID(id))
	return s.Get(ctx, m, id)
}
