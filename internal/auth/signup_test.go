package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/testutil"
)

// fakeSender records emails instead of sending them, so tests can assert on
// operator/owner notifications. Satisfies notification.Sender + auth.EmailSender.
type fakeSender struct {
	mu   sync.Mutex
	sent []sentEmail
}

type sentEmail struct{ To, Subject, Body string }

func (f *fakeSender) SendEmail(_ context.Context, to, subject string, body emaillayout.Email) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentEmail{To: to, Subject: subject, Body: body.HTML()})
	return nil
}

func (f *fakeSender) toList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, e := range f.sent {
		out[i] = e.To
	}
	return out
}

func newSignupRouter(pool *pgxpool.Pool, platformAdmins []string, sender *fakeSender) chi.Router {
	return api.NewRouter(api.Deps{
		Queries:             db.New(pool),
		DB:                  pool,
		JWTSecret:           testutil.TestJWTSecret,
		BaseURL:             "http://localhost:5173",
		DevAutoLogin:        true,
		EmailSender:         sender,
		PlatformAdminEmails: platformAdmins,
	})
}

// do issues a request with the given bearer token and returns status + parsed body.
func do(t *testing.T, r chi.Router, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		if c, ok := e["code"].(string); ok {
			return c
		}
	}
	return ""
}
