package notification_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

type nopSender struct{ err error }

func (s nopSender) SendEmail(context.Context, string, string, emaillayout.Email) error { return s.err }

func renderEmail(t *testing.T, heading string) emaillayout.Email {
	t.Helper()
	email, err := emaillayout.Render("", emaillayout.Content{Heading: heading})
	if err != nil {
		t.Fatal(err)
	}
	return email
}

func TestOutboxRecordsSentEmailsNewestFirst(t *testing.T) {
	ctx := context.Background()
	outbox := notif.NewOutbox(2)
	sender := &notif.RecordingSender{Next: nopSender{}, Outbox: outbox}
	for _, subject := range []string{"first", "second", "third"} {
		if err := sender.SendEmail(ctx, "Vendor@Example.com", subject, renderEmail(t, "body "+subject)); err != nil {
			t.Fatalf("send %s: %v", subject, err)
		}
	}
	got := outbox.For(" vendor@example.com ")
	if len(got) != 2 || got[0].Subject != "third" || got[1].Subject != "second" {
		t.Fatalf("want the 2 newest emails, newest first; got %+v", got)
	}
	if got[0].Body != renderEmail(t, "body third").HTML() {
		t.Errorf("recorded body is not the rendered email: %q", got[0].Body)
	}
	if len(outbox.For("someone-else@example.com")) != 0 {
		t.Error("an email to another address matched")
	}

	failing := &notif.RecordingSender{Next: nopSender{err: errors.New("down")}, Outbox: outbox}
	if err := failing.SendEmail(ctx, "bounce@example.com", "lost", renderEmail(t, "lost")); err == nil {
		t.Fatal("the send error was swallowed")
	}
	if len(outbox.For("bounce@example.com")) != 0 {
		t.Error("a failed send was recorded")
	}
}

// The outbox holds live invite and reset links, so its route exists only with
// both an outbox and DEV_AUTO_LOGIN, and needs a signed-in caller.
func TestOutboxRoute_GatedOnOutbox(t *testing.T) {
	pool, queries, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	userID, tenantID := testutil.RegisterUser(t, queries, "outbox")
	baseDeps := func() api.Deps {
		return api.Deps{
			Queries:   db.New(pool),
			DB:        pool,
			JWTSecret: testutil.TestJWTSecret,
			BaseURL:   "http://localhost:5173",
		}
	}

	prod := api.NewRouter(baseDeps())
	rec := testutil.DoRequest(t, prod, http.MethodGet, "/api/dev/outbox?to=a@example.com", nil, userID, tenantID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without an outbox: want 404, got %d", rec.Code)
	}

	outbox := notif.NewOutbox(10)
	sent := renderEmail(t, "link")
	if err := (&notif.RecordingSender{Next: nopSender{}, Outbox: outbox}).SendEmail(context.Background(), "a@example.com", "hi", sent); err != nil {
		t.Fatalf("send: %v", err)
	}
	// An outbox without the dev-login flag stays unmounted.
	noFlag := baseDeps()
	noFlag.DevOutbox = outbox
	rec = testutil.DoRequest(t, api.NewRouter(noFlag), http.MethodGet, "/api/dev/outbox?to=a@example.com", nil, userID, tenantID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outbox without DEV_AUTO_LOGIN: want 404, got %d", rec.Code)
	}

	deps := baseDeps()
	deps.DevOutbox = outbox
	deps.DevAutoLogin = true
	dev := api.NewRouter(deps)
	rec = testutil.DoRequest(t, dev, http.MethodGet, "/api/dev/outbox?to=a@example.com", nil, userID, tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("with an outbox: want 200, got %d — body: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Data []notif.OutboxEmail `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	if len(resp.Data) != 1 || resp.Data[0].Body != sent.HTML() {
		t.Errorf("outbox = %+v", resp.Data)
	}

	rec = testutil.DoRequest(t, dev, http.MethodGet, "/api/dev/outbox", nil, userID, tenantID)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("without ?to=: want 400, got %d", rec.Code)
	}
	rec = testutil.DoRequest(t, dev, http.MethodGet, "/api/dev/outbox?to=a@example.com", nil, 0, 0)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: want 401, got %d", rec.Code)
	}
}
