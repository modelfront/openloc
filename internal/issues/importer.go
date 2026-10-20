package issues

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/ptr"
)

// ViaImport marks an issue brought in from an outside list.
const ViaImport = "Import"

// ImportedIssue is one issue brought in from an outside list, such as a
// spreadsheet the support team kept before. Key names its source (a sheet and
// row) and is unique in the reporter's org, so importing the same source again
// creates nothing.
type ImportedIssue struct {
	Key                                      string
	Kind, Severity, Area, Title, Description string
	// Status is any status but duplicate. Confirmed (resolved only): the
	// reporter already checked the fix.
	Status    string
	Confirmed bool
	Priority  string // "" for none
	Tags      []string
	// AssigneeID must be a support team member.
	AssigneeID *int64
	Comments   []ImportedComment
	// Files go on the issue itself.
	Files []Upload
}

// ImportedComment is a comment of an imported issue: the reporter's, or an
// internal note of the support team with AuthorID as its author (nil: none).
// Its files go on the comment, so an internal note's stay with the support
// team. Body may be empty when there are files.
type ImportedComment struct {
	Body     string
	Internal bool
	AuthorID *int64
	Files    []Upload
}

// Import files an issue for the reporter as it stood in the outside list, with
// its comments and files. Nobody is notified and no rate limit applies: the
// deployment's operator runs it. It reports false, and changes nothing, when
// an earlier import already made the issue.
func (s *Service) Import(ctx context.Context, reporter Member, in ImportedIssue) (bool, error) {
	p, err := s.prepareImport(ctx, in)
	if err != nil {
		return false, err
	}
	c, tags, comments := p.fields, p.tags, p.comments
	q := db.New(s.pool)
	if _, err := q.GetIssueByImportKey(ctx, db.GetIssueByImportKeyParams{TenantID: reporter.TenantID, ImportKey: &in.Key}); err == nil {
		return false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("look up imported issue: %w", err)
	}
	issueContext, err := buildContext("", reporter.Role, ViaImport)
	if err != nil {
		return false, fmt.Errorf("build issue context: %w", err)
	}
	files, commentFiles, err := s.storeImportFiles(ctx, reporter.TenantID, in.Files, comments)
	if err != nil {
		return false, err
	}
	stored := files
	for _, f := range commentFiles {
		stored = append(stored, f...)
	}
	created := false
	_, err = s.inTx(ctx, func(q *db.Queries) (db.Issue, error) {
		issue, err := q.ImportIssue(ctx, db.ImportIssueParams{
			TenantID: reporter.TenantID, ReporterID: reporter.UserID, Kind: in.Kind, Severity: c.severity,
			Area: c.area, Title: c.title, Description: c.descr, Context: issueContext, Status: in.Status,
			Priority: ptr.NilIfZero(in.Priority), Tags: tags, AssigneeID: in.AssigneeID,
			LastActivityBy: importedLastActivity(in.Status), Confirmed: in.Confirmed, ImportKey: &in.Key,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Issue{}, nil // imported meanwhile
		}
		if err != nil {
			return db.Issue{}, fmt.Errorf("import issue: %w", err)
		}
		created = true
		for i, cm := range comments {
			if err := importComment(ctx, q, issue.ID, reporter.UserID, cm, commentFiles[i]); err != nil {
				return db.Issue{}, err
			}
		}
		_, err = recordFiles(ctx, q, issue.ID, nil, files)
		return issue, err
	})
	if err != nil || !created {
		s.dropFiles(ctx, stored)
		return false, err
	}
	logging.Info(ctx, "imported an issue", "issue-imported")
	return true, nil
}

// CheckImport validates an imported issue and stores nothing: the import
// command checks every row before it writes the first.
func (s *Service) CheckImport(ctx context.Context, in ImportedIssue) error {
	_, err := s.prepareImport(ctx, in)
	return err
}

// preparedImport is an imported issue's fields after validation.
type preparedImport struct {
	fields   checked
	tags     []string
	comments []preparedComment
}

// preparedComment is an imported comment after validation; body is nil for
// files alone.
type preparedComment struct {
	body     *string
	internal bool
	authorID *int64
	files    []Upload
}

// storeImportFiles stores the issue's files and each comment's, before the
// transaction that records them. On an error nothing stays stored.
func (s *Service) storeImportFiles(ctx context.Context, tenantID int64, issueFiles []Upload,
	comments []preparedComment) ([]storedFile, [][]storedFile, error) {
	files, err := s.storeFiles(ctx, tenantID, issueFiles)
	if err != nil {
		return nil, nil, err
	}
	stored := files
	perComment := make([][]storedFile, len(comments))
	for i, c := range comments {
		if perComment[i], err = s.storeFiles(ctx, tenantID, c.files); err != nil {
			s.dropFiles(ctx, stored)
			return nil, nil, err
		}
		stored = append(stored, perComment[i]...)
	}
	return files, perComment, nil
}

func (s *Service) prepareImport(ctx context.Context, in ImportedIssue) (preparedImport, error) {
	if err := s.checkImport(ctx, in); err != nil {
		return preparedImport{}, err
	}
	c, err := NewIssue{Kind: in.Kind, Severity: in.Severity, Area: in.Area, Title: in.Title,
		Description: in.Description, Files: in.Files}.check()
	if err != nil {
		return preparedImport{}, err
	}
	tags, err := cleanTags(in.Tags)
	if err != nil {
		return preparedImport{}, err
	}
	comments, err := cleanImportedComments(in.Comments)
	if err != nil {
		return preparedImport{}, err
	}
	return preparedImport{fields: c, tags: tags, comments: comments}, nil
}

// checkImport refuses what an imported issue cannot be.
func (s *Service) checkImport(ctx context.Context, in ImportedIssue) error {
	if in.Key == "" || len(in.Key) > 200 {
		return clientError("An imported issue needs a key of 1 to 200 characters.")
	}
	if statusLabels[in.Status] == "" || in.Status == StatusDuplicate {
		return clientError("Unknown status for an imported issue: " + in.Status)
	}
	if in.Confirmed && in.Status != StatusResolved {
		return clientError("Only a resolved issue can be confirmed.")
	}
	if in.Priority != "" && priorityLabels[in.Priority] == "" {
		return clientError("Unknown priority: " + in.Priority)
	}
	if in.AssigneeID != nil {
		if err := s.checkAssignee(ctx, *in.AssigneeID); err != nil {
			return err
		}
	}
	for _, c := range in.Comments {
		if c.AuthorID != nil && c.Internal {
			if err := s.checkAssignee(ctx, *c.AuthorID); err != nil {
				return clientError("An internal note's author must be a member of the support team.")
			}
		}
	}
	return nil
}

func cleanImportedComments(in []ImportedComment) ([]preparedComment, error) {
	out := make([]preparedComment, 0, len(in))
	for _, c := range in {
		body, err := commentBody(NewComment{Body: c.Body, Files: c.Files})
		if err != nil {
			return nil, err
		}
		out = append(out, preparedComment{body: body, internal: c.Internal, authorID: c.AuthorID, files: c.Files})
	}
	return out, nil
}

// importComment records an imported comment with its stored files: the
// reporter's, or an internal note.
func importComment(ctx context.Context, q *db.Queries, issueID, reporterID int64, c preparedComment, files []storedFile) error {
	if c.internal {
		return addNote(ctx, q, issueID, c.authorID, c.body, files)
	}
	row, err := q.CreateIssueComment(ctx, db.CreateIssueCommentParams{
		IssueID: issueID, AuthorID: &reporterID, AuthorSide: sideReporter, Body: c.body,
	})
	if err != nil {
		return fmt.Errorf("create imported comment: %w", err)
	}
	_, err = recordFiles(ctx, q, issueID, &row.ID, files)
	return err
}

// importedLastActivity: an issue that needs info waits on the reporter's org;
// any other waits on the support team.
func importedLastActivity(status string) string {
	if status == StatusNeedsInfo {
		return sideOperator
	}
	return sideReporter
}
