package issues

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/logging"
)

var errNoStorage = errors.New("file storage is not available")

// dropTimeout bounds removing files whose issue or comment was not saved.
const dropTimeout = 30 * time.Second

// storedFile is an attachment written to storage, not yet recorded.
type storedFile struct {
	name, contentType, key string
	size                   int64
}

// storeFiles writes the uploads to storage. On any failure it removes what it
// already wrote, so nothing is left without a row.
func (s *Service) storeFiles(ctx context.Context, tenantID int64, files []Upload) ([]storedFile, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if s.store == nil {
		return nil, errNoStorage
	}
	var stored []storedFile
	for _, f := range files {
		sf, err := s.storeFile(ctx, tenantID, f)
		if err != nil {
			s.dropFiles(ctx, stored)
			return nil, err
		}
		stored = append(stored, sf)
	}
	return stored, nil
}

func (s *Service) storeFile(ctx context.Context, tenantID int64, f Upload) (storedFile, error) {
	rc, err := f.Open()
	if err != nil {
		return storedFile{}, fmt.Errorf("open attachment: %w", err)
	}
	defer rc.Close()
	contentType, err := sniff(rc)
	if err != nil {
		return storedFile{}, fmt.Errorf("read attachment: %w", err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return storedFile{}, fmt.Errorf("attachment key: %w", err)
	}
	key := fmt.Sprintf("issues/%d/%s", tenantID, hex.EncodeToString(b))
	if err := s.store.Put(ctx, key, rc); err != nil {
		return storedFile{}, fmt.Errorf("store attachment: %w", err)
	}
	return storedFile{name: cleanFilename(f.Name), contentType: contentType, key: key, size: f.Size}, nil
}

// recordFiles adds the attachment rows, inside the caller's transaction, and
// returns their views.
func recordFiles(ctx context.Context, q *db.Queries, issueID int64, commentID *int64, files []storedFile) ([]AttachmentView, error) {
	views := make([]AttachmentView, 0, len(files))
	for _, f := range files {
		row, err := q.CreateIssueAttachment(ctx, db.CreateIssueAttachmentParams{
			IssueID: issueID, CommentID: commentID, Filename: f.name, ContentType: f.contentType,
			SizeBytes: f.size, StorageKey: f.key,
		})
		if err != nil {
			return nil, fmt.Errorf("record attachment: %w", err)
		}
		views = append(views, attachmentView(row))
	}
	return views, nil
}

// dropFiles removes stored files whose issue or comment was not saved.
func (s *Service) dropFiles(ctx context.Context, files []storedFile) {
	if len(files) == 0 || s.store == nil {
		return
	}
	keys := make([]string, 0, len(files))
	for _, f := range files {
		keys = append(keys, f.key)
	}
	// Detached, so a cancelled request still cleans up; bounded, because the
	// storage client has no timeout of its own.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), dropTimeout)
	defer cancel()
	if err := s.store.DeleteKeys(cleanup, keys); err != nil {
		logging.Error(ctx, err, "removing unsaved issue attachments failed", "issue-attachment-orphan-delete-failed")
	}
}

// Attachment is a stored file ready to send. The caller closes Body.
type Attachment struct {
	Filename, ContentType string
	Size                  int64
	IsImage               bool
	Body                  io.ReadCloser
}

// openAttachment reads a stored file. The caller has already checked access
// to the issue and loaded the file's row through a query for its side.
func (s *Service) openAttachment(ctx context.Context, f db.IssueAttachment) (Attachment, error) {
	if s.store == nil {
		return Attachment{}, pgx.ErrNoRows
	}
	rc, err := s.store.Download(ctx, f.StorageKey)
	if errors.Is(err, file.ErrBlobNotFound) {
		return Attachment{}, pgx.ErrNoRows
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("download attachment: %w", err)
	}
	return Attachment{Filename: f.Filename, ContentType: f.ContentType, Size: f.SizeBytes,
		IsImage: imageTypes[f.ContentType], Body: rc}, nil
}
