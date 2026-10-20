package issues

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/httputil"
	"openloc/internal/logging"
	"openloc/internal/notification"
	"openloc/internal/signedtoken"
	"openloc/internal/sliceutil"
)

// How many issues one user may file in an hour.
const (
	rateLimit       = 10
	rateLimitWindow = time.Hour
	// How many comments one member may write in an hour. Operators are not
	// limited.
	commentRateLimit = 30
)

// Service files issues, runs both sides of their conversation and notifies the
// other side.
type Service struct {
	pool      *pgxpool.Pool
	store     file.BlobStore // nil: issues without attachments only
	notifier  *notification.Service
	sender    notification.Sender
	operators []string      // platform admin emails, as configured
	opSet     auth.EmailSet // the same, as a set: who may be assigned
	queue     TaskEnqueuer  // nil: operator emails are sent in the request
	baseURL   string
	// verifyKey signs the assistant's checks of the reports it writes.
	verifyKey []byte
	now       func() time.Time
}

// NewService builds the service. store, notifier and sender may be nil.
func NewService(pool *pgxpool.Pool, store file.BlobStore, notifier *notification.Service,
	sender notification.Sender, operators []string, baseURL string) *Service {
	return &Service{pool: pool, store: store, notifier: notifier, sender: sender,
		operators: operators, opSet: auth.NewEmailSet(operators), baseURL: baseURL,
		verifyKey: signedtoken.RandomKey(), now: time.Now}
}

// Enabled reports whether anyone receives issues: a deployment without
// platform admins has nobody to read them.
func (s *Service) Enabled() bool { return len(s.operators) > 0 }

// Member is the session's user, org and role: who files an issue or reads
// the org's issues.
type Member struct {
	TenantID, UserID int64
	Role             string
}

// seesAllMembers: managers see every issue of their org, anyone else only the
// issues they reported.
func (m Member) seesAllMembers() bool { return auth.IsManagerRole(m.Role) }

// mayDelete: their own issue; an owner, any issue of the org; anyone else an
// issue whose reporter now ranks below them. A reporter who left the org ranks
// lowest. Same-rank colleagues (owners aside) leave each other's alone.
func (m Member) mayDelete(reporterID int64, reporterRole string) bool {
	return reporterID == m.UserID || m.Role == auth.RoleOwner || auth.RoleAbove(m.Role, reporterRole)
}

// errMayNotDelete: the member sees the issue but may not delete it.
var errMayNotDelete = httputil.NewForbiddenError("You can delete your own issues and those reported by a role below yours.")

// NewIssue is what the reporter filled in. Context is the client's JSON. Via
// is set by the server when an issue does not come from the app's form (the AI
// assistant); it is kept in the context for the operators. Verification is the
// assistant's check of a report it wrote, kept only with a VerificationToken
// from SignVerification. Trail is the client's action trail and Recording the
// watch-mode recording (JSON), both kept for the support team alone.
type NewIssue struct {
	Kind, Severity, Area, Title, Description, Context string
	Via                                               string
	Files                                             []Upload
	Verification                                      Verification
	VerificationToken                                 string
	Trail, Recording                                  string
}

// checked is a NewIssue's fields after validation.
type checked struct {
	severity           *string
	area, title, descr string
}

// check validates what the reporter typed, as Create does before it stores
// anything.
func (in NewIssue) check() (checked, error) {
	if kindLabels[in.Kind] == "" {
		return checked{}, clientError("Choose a bug, an enhancement or a feature request.")
	}
	severity, area, err := triage(in.Kind, in.Severity, in.Area)
	if err != nil {
		return checked{}, err
	}
	title, err := cleanText(in.Title, "A title", 1, maxTitle)
	if err != nil {
		return checked{}, err
	}
	description, err := cleanText(in.Description, "A description", 1, maxText)
	if err != nil {
		return checked{}, err
	}
	if err := checkUploads(in.Files); err != nil {
		return checked{}, err
	}
	if err := checkUploadRefs(description, in.Files); err != nil {
		return checked{}, err
	}
	return checked{severity: severity, area: area, title: title, descr: description}, nil
}

// Check validates an issue and the assistant's check of it without filing it:
// what the assistant's preview runs, so the user never confirms a report that
// would be refused.
func (s *Service) Check(in NewIssue) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if _, err := in.check(); err != nil {
		return err
	}
	_, err := in.Verification.clean()
	return err
}

// ErrDisabled: the deployment has no platform operators, so nobody would read
// an issue.
var ErrDisabled = httputil.NewClientError("This OpenLoc installation does not take issue reports.")

// rateLimitedError is a per-member limit reached: msg says which.
type rateLimitedError struct{ msg string }

var (
	errTooManyIssues   = rateLimitedError{"You have reported many issues in the last hour. Please try again later."}
	errTooManyComments = rateLimitedError{"You have written many comments in the last hour. Please try again later."}
)

// IsRateLimited reports whether err is a per-member limit reached.
func IsRateLimited(err error) bool {
	var r rateLimitedError
	return errors.As(err, &r)
}

func (e rateLimitedError) Error() string   { return e.msg }
func (rateLimitedError) StatusCode() int   { return http.StatusTooManyRequests }
func (rateLimitedError) ErrorCode() string { return "issue_rate_limited" }

// Create files an issue with its files and emails the operators.
func (s *Service) Create(ctx context.Context, m Member, in NewIssue) (IssueView, error) {
	if !s.Enabled() {
		return IssueView{}, ErrDisabled
	}
	c, err := in.check()
	if err != nil {
		return IssueView{}, err
	}
	issueContext, err := buildContext(in.Context, m.Role, in.Via)
	if err != nil {
		return IssueView{}, fmt.Errorf("build issue context: %w", err)
	}
	recent, err := db.New(s.pool).CountRecentIssuesByReporter(ctx, db.CountRecentIssuesByReporterParams{
		ReporterID: m.UserID, Since: timestamptz(s.now().Add(-rateLimitWindow)),
	})
	if err != nil {
		return IssueView{}, fmt.Errorf("count recent issues: %w", err)
	}
	if recent >= rateLimit {
		return IssueView{}, errTooManyIssues
	}

	files, err := s.storeFiles(ctx, m.TenantID, in.Files)
	if err != nil {
		return IssueView{}, err
	}
	var attachments []AttachmentView
	check := s.acceptedVerification(ctx, m, in.Verification, in.VerificationToken)
	trail, rec := cleanTrail(in.Trail), cleanRecording(in.Recording)
	insert := func(check acceptedCheck) (db.Issue, error) {
		verdict, verdictNote, tokenID := check.columns()
		return s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
			issue, err := q.CreateIssue(ctx, db.CreateIssueParams{
				TenantID: m.TenantID, ReporterID: m.UserID, Kind: in.Kind, Severity: c.severity, Area: c.area,
				Title: c.title, Description: c.descr, Context: issueContext,
				ChatVerification: verdict, ChatVerificationNote: verdictNote, ChatVerificationTokenID: tokenID,
			})
			if err != nil {
				return db.Issue{}, fmt.Errorf("create issue: %w", err)
			}
			if trail != nil || rec != nil {
				if err := q.CreateIssueCapture(ctx, db.CreateIssueCaptureParams{IssueID: issue.ID, Trail: trail, Recording: rec}); err != nil {
					return db.Issue{}, fmt.Errorf("save issue capture: %w", err)
				}
			}
			if attachments, err = recordFiles(ctx, q, issue.ID, nil, files); err != nil {
				return db.Issue{}, err
			}
			return linkDescriptionImages(ctx, q, issue, attachments)
		})
	}
	issue, err := insert(check)
	if check.tokenID != "" && httputil.IsUniqueViolationOn(err, "issues_verification_token_idx") {
		// The token already proved a check on another report: this one goes
		// without, like any report whose token does not hold.
		logging.Warn(ctx, nil, "dropped an issue verification whose token was already used", "issue-verification-reused",
			logging.Label("issue.verification", check.Result))
		issue, err = insert(acceptedCheck{})
	}
	if err != nil {
		s.dropFiles(ctx, files)
		return IssueView{}, err
	}
	logging.Info(ctx, "reported an issue", "issue-created", logging.IssueID(issue.ID))
	s.emailOperatorsNewIssue(ctx, issue)
	view := issueView(issue)
	view.Attachments = attachments
	view.CanDelete = m.mayDelete(issue.ReporterID, m.Role)
	return view, nil
}

// List is the org's issues the member may see, filtered and ordered by f,
// from cursor on.
func (s *Service) List(ctx context.Context, m Member, f Filter, cursor Cursor, limit int32) ([]IssueView, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	p := db.ListMemberIssuesNewestParams{
		TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
		Statuses: sliceutil.NonNil(f.Statuses), Kinds: sliceutil.NonNil(f.Kinds), Areas: sliceutil.NonNil(f.Areas),
		Severities: sliceutil.NonNil(f.Severities), Search: likePattern(f.Search),
		CursorAt: cursor.at(), CursorID: cursor.ID, RowLimit: limit,
	}
	q := db.New(s.pool)
	var rows []db.ListMemberIssuesNewestRow
	var err error
	if f.OldestFirst {
		var old []db.ListMemberIssuesOldestRow
		old, err = q.ListMemberIssuesOldest(ctx, db.ListMemberIssuesOldestParams(p))
		for _, r := range old {
			rows = append(rows, db.ListMemberIssuesNewestRow(r))
		}
	} else {
		rows, err = q.ListMemberIssuesNewest(ctx, p)
	}
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	out := make([]IssueView, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueRow(r).memberView(m))
	}
	return out, nil
}

// Get is one of the issues the member may see, with its conversation and files.
func (s *Service) Get(ctx context.Context, m Member, id int64) (IssueDetail, error) {
	q := db.New(s.pool)
	issue, err := q.GetMemberIssue(ctx, db.GetMemberIssueParams{
		ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
	})
	if err != nil {
		return IssueDetail{}, err
	}
	rows, err := q.ListMemberIssueComments(ctx, db.ListMemberIssueCommentsParams{
		IssueID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
	})
	if err != nil {
		return IssueDetail{}, fmt.Errorf("list issue comments: %w", err)
	}
	comments := make([]commentRow, 0, len(rows))
	for _, r := range rows {
		comments = append(comments, commentRow(r))
	}
	t, err := s.memberThread(ctx, id, comments)
	if err != nil {
		return IssueDetail{}, err
	}
	view := issueRow(issue).memberView(m)
	view.Attachments = t.files
	return IssueDetail{Issue: view, Comments: t.comments}, nil
}

// Delete soft-deletes one of the issues the member sees, if mayDelete allows.
func (s *Service) Delete(ctx context.Context, m Member, id int64) error {
	q := db.New(s.pool)
	issue, err := q.GetMemberIssue(ctx, db.GetMemberIssueParams{
		ID: id, TenantID: m.TenantID, ViewerID: m.UserID, AllMembers: m.seesAllMembers(),
	})
	if err != nil {
		return err
	}
	if !m.mayDelete(issue.ReporterID, issue.ReporterRole) {
		return errMayNotDelete
	}
	err = s.softDelete(ctx, id, func(q *db.Queries) (int64, error) {
		return q.SoftDeleteMemberIssue(ctx, db.SoftDeleteMemberIssueParams{ID: id, TenantID: m.TenantID})
	})
	if err != nil {
		return err
	}
	logging.Info(ctx, "deleted an issue", "issue-deleted", logging.IssueID(id))
	return nil
}

// softDelete runs del, which soft-deletes issue id, and sends the issue's
// duplicates back to the support team's queue, each with an entry its org
// sees: with their original gone, nothing tracks their problem. It holds the
// duplicate lock, so no request picks the issue as an original meanwhile.
func (s *Service) softDelete(ctx context.Context, id int64, del func(q *db.Queries) (int64, error)) error {
	_, err := s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		if err := q.LockIssueDuplicates(ctx); err != nil {
			return db.Issue{}, fmt.Errorf("lock duplicates: %w", err)
		}
		n, err := del(q)
		if err != nil {
			return db.Issue{}, fmt.Errorf("delete issue: %w", err)
		}
		if n == 0 {
			return db.Issue{}, pgx.ErrNoRows
		}
		if err := q.ReopenDuplicatesOf(ctx, id); err != nil {
			return db.Issue{}, fmt.Errorf("reopen duplicates: %w", err)
		}
		return db.Issue{}, nil
	})
	return err
}

// inTx runs fn in a transaction and returns the issue it produced.
func (s *Service) inTx(ctx context.Context, fn func(q *db.Queries) (db.Issue, error)) (db.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Issue{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op after Commit
	issue, err := fn(db.New(tx))
	if err != nil {
		return db.Issue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Issue{}, fmt.Errorf("commit: %w", err)
	}
	return issue, nil
}

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func clientError(msg string) error { return httputil.NewClientError(msg) }
