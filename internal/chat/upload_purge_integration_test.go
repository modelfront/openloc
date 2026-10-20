package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"openloc/internal/chat"
	"openloc/internal/file"
)

// uploadKey uploads a file through the handler and returns its row id and
// storage key.
func (f *chatFixture) uploadKey(t *testing.T, tenantID int64, name string) (string, string) {
	t.Helper()
	user := f.ownerA
	if tenantID == f.tenantB.ID {
		user = f.ownerB
	}
	rec := f.upload(t, user, tenantID, name, []byte("<tmx/>"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT storage_key FROM chat_uploads WHERE id = $1`, env.Data.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return env.Data.ID, key
}

func (f *chatFixture) blobExists(t *testing.T, key string) bool {
	t.Helper()
	rc, err := f.store.Download(context.Background(), key)
	if errors.Is(err, file.ErrBlobNotFound) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	return true
}

func (f *chatFixture) rowExists(t *testing.T, id string) bool {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat_uploads WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// The sweep deletes an expired upload's row and blob and keeps a live one.
func TestIntegrationChatUploadPurgeRemovesExpired(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	expiredID, expiredKey := f.uploadKey(t, f.tenantA.ID, "old.tmx")
	liveID, liveKey := f.uploadKey(t, f.tenantA.ID, "new.tmx")
	if _, err := f.pool.Exec(ctx, `UPDATE chat_uploads SET expires_at = now() - interval '1 minute' WHERE id = $1`, expiredID); err != nil {
		t.Fatal(err)
	}

	purger := chat.NewUploadPurger(f.q, f.store)
	rows, blobs, err := purger.Purge(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if rows != 1 || blobs != 1 {
		t.Errorf("purged rows=%d blobs=%d, want 1 and 1", rows, blobs)
	}
	if f.rowExists(t, expiredID) || f.blobExists(t, expiredKey) {
		t.Error("the expired upload survived the sweep")
	}
	if !f.rowExists(t, liveID) || !f.blobExists(t, liveKey) {
		t.Error("the sweep deleted a live upload")
	}

	// A second run finds nothing left to do.
	if rows, blobs, err := purger.Purge(ctx); err != nil || rows != 0 || blobs != 0 {
		t.Errorf("second purge: rows=%d blobs=%d err=%v, want nothing", rows, blobs, err)
	}
}

// Deleting a tenant cascades its upload rows; the sweep then deletes their
// blobs and leaves other tenants' uploads alone.
func TestIntegrationChatUploadPurgeAfterTenantDelete(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	_, keyA := f.uploadKey(t, f.tenantA.ID, "a.tmx")
	idB, keyB := f.uploadKey(t, f.tenantB.ID, "b.tmx")

	if err := f.q.DeleteTenant(ctx, f.tenantA.ID); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	if _, _, err := chat.NewUploadPurger(f.q, f.store).Purge(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if f.blobExists(t, keyA) {
		t.Error("the deleted tenant's upload blob is still stored")
	}
	if !f.rowExists(t, idB) || !f.blobExists(t, keyB) {
		t.Error("another tenant's upload was deleted")
	}
}

// failingStore refuses every delete.
type failingStore struct{}

func (failingStore) DeleteKeys(context.Context, []string) error { return errors.New("storage down") }

// A failed blob delete keeps the key queued, so the next run deletes it.
func TestIntegrationChatUploadPurgeRetriesFailedBlobDelete(t *testing.T) {
	f := newChatFixture(t, nil)
	ctx := context.Background()
	id, key := f.uploadKey(t, f.tenantA.ID, "retry.tmx")
	if _, err := f.pool.Exec(ctx, `UPDATE chat_uploads SET expires_at = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chat.NewUploadPurger(f.q, failingStore{}).Purge(ctx); err == nil {
		t.Fatal("purge with failing storage returned no error")
	}
	if !f.blobExists(t, key) {
		t.Fatal("blob gone although storage refused the delete")
	}
	if _, blobs, err := chat.NewUploadPurger(f.q, f.store).Purge(ctx); err != nil || blobs != 1 {
		t.Fatalf("retry purge: blobs=%d err=%v, want 1", blobs, err)
	}
	if f.blobExists(t, key) {
		t.Error("the retried purge left the blob")
	}
}
