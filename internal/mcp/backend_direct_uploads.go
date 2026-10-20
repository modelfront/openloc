package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "openloc/internal/database/sqlcgen"
)

// UploadRef is embedded in the input of a tool that imports a file the user
// attached in the assistant panel.
type UploadRef struct {
	UploadID string `json:"upload_id" jsonschema:"id of a file the user attached in the assistant panel; the attachment note in their message carries it"`
}

// openUpload returns an upload of the calling user, or errNotFound — the same
// answer for a foreign, expired or made-up id. The caller closes the reader.
func (b *directBackend) openUpload(ctx context.Context, uploadID string) (db.ChatUpload, io.ReadCloser, error) {
	if b.storage == nil {
		return db.ChatUpload{}, nil, invalidf("file storage is not available on this server")
	}
	id, err := uuid.Parse(uploadID)
	if err != nil {
		return db.ChatUpload{}, nil, errNotFound
	}
	tenantID, userID := scope(ctx)
	row, err := b.q.GetChatUpload(ctx, db.GetChatUploadParams{
		ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenantID, UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.ChatUpload{}, nil, errNotFound
		}
		return db.ChatUpload{}, nil, fmt.Errorf("get upload: %w", err)
	}
	body, err := b.storage.Download(ctx, row.StorageKey)
	if err != nil {
		return db.ChatUpload{}, nil, fmt.Errorf("download upload: %w", err)
	}
	return row, body, nil
}
