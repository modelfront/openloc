package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

func (f *chatFixture) upload(t *testing.T, user db.User, tenantID int64, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/chat/uploads", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantID, UserID: user.ID, Role: "owner"}))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// An upload is usable only by its owner, in their tenant, until it expires.
func TestIntegrationChatUploadIsOwnerScoped(t *testing.T) {
	f := newChatFixture(t, nil)
	rec := f.upload(t, f.ownerA, f.tenantA.ID, "../../etc/terms.tbx", []byte("<martif/>"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			ID        string `json:"id"`
			Filename  string `json:"filename"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Filename != "terms.tbx" || env.Data.SizeBytes != 9 {
		t.Errorf("upload view = %+v (path must be stripped)", env.Data)
	}
	id := pgtype.UUID{Bytes: uuid.MustParse(env.Data.ID), Valid: true}
	get := func(tenantID, userID int64) error {
		_, err := f.q.GetChatUpload(context.Background(), db.GetChatUploadParams{ID: id, TenantID: tenantID, UserID: userID})
		return err
	}

	if err := get(f.tenantA.ID, f.ownerA.ID); err != nil {
		t.Fatalf("owner cannot read their upload: %v", err)
	}
	colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "colleague@test.com", "pass", "owner")
	if err := get(f.tenantA.ID, colleague.ID); err == nil {
		t.Error("another user in the same tenant read the upload")
	}
	if err := get(f.tenantB.ID, f.ownerA.ID); err == nil {
		t.Error("the upload was readable under another tenant")
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE chat_uploads SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := get(f.tenantA.ID, f.ownerA.ID); err == nil {
		t.Error("an expired upload was still readable")
	}

}

// The limit is the configured one, not a fixed 50 MB: a file past it is refused
// and the message names the limit.
func TestIntegrationChatUploadRefusesPastTheConfiguredLimit(t *testing.T) {
	f := newChatFixture(t, nil)
	f.handler.SetMaxUploadBytes(2 << 20)
	rec := f.upload(t, f.ownerA, f.tenantA.ID, "big.tbx", bytes.Repeat([]byte("x"), 3<<20))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "max 2 MB") {
		t.Fatalf("upload past the limit: %d %s, want 400 naming max 2 MB", rec.Code, rec.Body.String())
	}
}

// A stored file whose row cannot be written is removed again: without a row
// the purge never learns its key, and the file would outlive its 24 hours.
func TestIntegrationChatUploadRemovesTheBlobWhenTheRowFails(t *testing.T) {
	f := newChatFixture(t, nil)
	ghost := db.User{ID: f.ownerA.ID + 1_000_000} // no such user: the insert breaks its foreign key
	rec := f.upload(t, ghost, f.tenantA.ID, "terms.tbx", []byte("<martif/>"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("upload by a missing user: %d %s, want 500", rec.Code, rec.Body.String())
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("%d blob(s) left in storage after the row failed", n)
	}
}
