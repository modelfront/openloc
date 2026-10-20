package qa

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// ErrNoQAFinding is returned when an ignore/un-ignore targets a check that has
// no current finding on the segment. Callers map it to 404.
var ErrNoQAFinding = errors.New("no such QA finding on this segment")

// markIgnored copies the ignore state from the stored qa_results rows onto the
// freshly computed issues. A fresh issue is ignored only when the stored row for
// the same check_type is ignored AND has the same message — a changed message is
// a different finding and must surface as new.
func markIgnored(issues []Issue, stored []db.ListQAResultsForTranslationRow) []Issue {
	ignoredMsg := make(map[string]string, len(stored))
	for _, row := range stored {
		if row.Ignored {
			ignoredMsg[row.CheckType] = row.Message
		}
	}
	return applyIgnored(issues, ignoredMsg)
}

// applyIgnored marks the issues whose check_type has an ignored stored finding
// (check_type → message) with the same message.
func applyIgnored(issues []Issue, ignoredMsg map[string]string) []Issue {
	if len(ignoredMsg) == 0 || len(issues) == 0 {
		return issues
	}
	for i := range issues {
		if msg, ok := ignoredMsg[issues[i].CheckType]; ok && msg == issues[i].Message {
			issues[i].Ignored = true
		}
	}
	return issues
}

// SetIgnoredParams targets one current finding on one segment.
type SetIgnoredParams struct {
	TranslationID int64
	TenantID      int64
	UserID        int64
	CheckType     string
	Ignored       bool
}

// SetIgnored flips the ignore flag on a stored finding and returns the
// segment's full finding list afterwards, so HTTP and MCP callers hand the
// client the same fresh state. Shared by both surfaces so they cannot drift.
func (s *Service) SetIgnored(ctx context.Context, p SetIgnoredParams) ([]db.ListQAResultsForTranslationRow, error) {
	if _, err := s.queries.SetQAResultIgnored(ctx, db.SetQAResultIgnoredParams{
		Ignored:       p.Ignored,
		UserID:        p.UserID,
		TranslationID: p.TranslationID,
		CheckType:     p.CheckType,
		TenantID:      p.TenantID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoQAFinding
		}
		return nil, fmt.Errorf("set QA result ignored: %w", err)
	}
	rows, err := s.queries.ListQAResultsForTranslation(ctx, p.TranslationID)
	if err != nil {
		return nil, fmt.Errorf("list QA results: %w", err)
	}
	return rows, nil
}
