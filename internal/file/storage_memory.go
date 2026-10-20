package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrBlobNotFound is a Download of a key nothing was stored under.
var ErrBlobNotFound = errors.New("blob not found")

// BlobStore is what the upload and publish paths need from file storage:
// keep a source file, hand it back later. *Storage (S3) is the
// production implementation; MemoryStore serves tests.
type BlobStore interface {
	Upload(ctx context.Context, projectID int64, filename string, body io.Reader) (string, error)
	// Put stores under a caller-chosen key, outside the projects/<id>/ layout
	// (assistant uploads).
	Put(ctx context.Context, key string, body io.Reader) error
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	DeleteKeys(ctx context.Context, keys []string) error
}

// MemoryStore keeps uploads in a map. Tests that publish a draft need the
// source back to materialize its jobs, which a nil store cannot give them.
type MemoryStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{blobs: map[string][]byte{}}
}

func (m *MemoryStore) Upload(_ context.Context, projectID int64, filename string, body io.Reader) (string, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("projects/%d/%d-%s", projectID, len(m.blobs), filename)
	m.blobs[key] = content
	return key, nil
}

func (m *MemoryStore) Put(_ context.Context, key string, body io.Reader) error {
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[key] = content
	return nil
}

func (m *MemoryStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	content, ok := m.blobs[key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("memory store: no blob at %q: %w", key, ErrBlobNotFound)
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// DeleteKeys removes the given keys; a key that is not there is not an error,
// matching S3 DeleteObjects.
func (m *MemoryStore) DeleteKeys(_ context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.blobs, k)
	}
	return nil
}

// Len is the number of stored blobs.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blobs)
}
