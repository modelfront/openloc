package comments

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
)

// ErrNotFound is returned for a translation or comment the caller cannot
// reach — absent, soft-deleted, in another tenant, or not theirs to delete.
var ErrNotFound = errors.New("not found")

// Service holds the comment reads and writes shared by the REST handler and
// the MCP tools. Every method resolves the translation tenant-scoped first;
// the comment queries then run only on that translation id.
type Service struct {
	queries db.Querier
}

func NewService(q db.Querier) *Service { return &Service{queries: q} }

func (s *Service) checkTranslation(ctx context.Context, tenantID, translationID int64) error {
	_, err := s.queries.GetTranslationTenant(ctx, db.GetTranslationTenantParams{ID: translationID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get translation: %w", err)
	}
	return nil
}

// List returns the live comments on one translation, oldest first.
func (s *Service) List(ctx context.Context, tenantID, translationID int64) ([]db.ListCommentsByTranslationRow, error) {
	if err := s.checkTranslation(ctx, tenantID, translationID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListCommentsByTranslation(ctx, translationID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	return rows, nil
}

// Get returns one live comment on one translation.
func (s *Service) Get(ctx context.Context, tenantID, translationID, commentID int64) (db.GetCommentOnTranslationRow, error) {
	if err := s.checkTranslation(ctx, tenantID, translationID); err != nil {
		return db.GetCommentOnTranslationRow{}, err
	}
	c, err := s.queries.GetCommentOnTranslation(ctx, db.GetCommentOnTranslationParams{ID: commentID, TranslationID: translationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetCommentOnTranslationRow{}, ErrNotFound
	}
	if err != nil {
		return db.GetCommentOnTranslationRow{}, fmt.Errorf("get comment: %w", err)
	}
	return c, nil
}

// Create adds a comment by userID to one translation.
func (s *Service) Create(ctx context.Context, tenantID, userID, translationID int64, content string) (db.SegmentComment, error) {
	if err := s.checkTranslation(ctx, tenantID, translationID); err != nil {
		return db.SegmentComment{}, err
	}
	c, err := s.queries.CreateComment(ctx, db.CreateCommentParams{
		TranslationID: translationID,
		UserID:        &userID,
		Content:       content,
	})
	if err != nil {
		return db.SegmentComment{}, fmt.Errorf("create comment: %w", err)
	}
	return c, nil
}

// MayDelete reports whether userID may delete the comment: their own, or an
// AI-authored one (no author).
func MayDelete(c db.GetCommentOnTranslationRow, userID int64) bool {
	return c.UserID == nil || *c.UserID == userID
}

// Delete soft-deletes one comment. ErrNotFound when nothing matched —
// including someone else's comment, which a user may not delete.
func (s *Service) Delete(ctx context.Context, tenantID, userID, translationID, commentID int64) error {
	if err := s.checkTranslation(ctx, tenantID, translationID); err != nil {
		return err
	}
	n, err := s.queries.SoftDeleteComment(ctx, db.SoftDeleteCommentParams{
		ID: commentID, TranslationID: translationID, UserID: &userID,
	})
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
