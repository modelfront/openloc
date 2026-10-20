package notification

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"openloc/internal/emaillayout"
	"openloc/internal/httputil"
)

// Outbox keeps the most recent emails sent, in memory, so E2E tests can follow
// the links they carry: invite and password-reset links exist nowhere else.
// Dev only — main.go builds one only when DEV_AUTO_LOGIN is on, because the
// emails hold live tokens.
type Outbox struct {
	mu       sync.Mutex
	capacity int
	emails   []OutboxEmail
}

// OutboxEmail is one recorded email.
type OutboxEmail struct {
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	SentAt  time.Time `json:"sent_at"`
	// ThreadID and ThreadRoot are the email's thread (emaillayout.Thread).
	ThreadID   string `json:"thread_id,omitempty"`
	ThreadRoot bool   `json:"thread_root,omitempty"`
}

// NewOutbox returns an Outbox holding at most capacity emails; older ones are
// dropped first.
func NewOutbox(capacity int) *Outbox {
	return &Outbox{capacity: capacity}
}

func (o *Outbox) record(to, subject string, body emaillayout.Email) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := body.Thread()
	o.emails = append(o.emails, OutboxEmail{To: to, Subject: subject, Body: body.HTML(), SentAt: time.Now(),
		ThreadID: t.ID, ThreadRoot: t.Root})
	if len(o.emails) > o.capacity {
		o.emails = o.emails[len(o.emails)-o.capacity:]
	}
}

// For returns the recorded emails to an address, newest first. The address
// matches case-insensitively, the way sign-in matches emails.
func (o *Outbox) For(to string) []OutboxEmail {
	to = strings.ToLower(strings.TrimSpace(to))
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]OutboxEmail, 0)
	for i := len(o.emails) - 1; i >= 0; i-- {
		if strings.ToLower(o.emails[i].To) == to {
			out = append(out, o.emails[i])
		}
	}
	return out
}

// HandleList serves GET /api/dev/outbox?to=<address>: the recorded emails to
// that address, newest first.
func (o *Outbox) HandleList(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if strings.TrimSpace(to) == "" {
		httputil.BadRequest(w, "to is required")
		return
	}
	httputil.Data(w, o.For(to))
}

// RecordingSender sends through Next and records every email it sent in
// Outbox. A failed send is not recorded.
type RecordingSender struct {
	Next   Sender
	Outbox *Outbox
}

func (s *RecordingSender) SendEmail(ctx context.Context, to, subject string, body emaillayout.Email) error {
	if err := s.Next.SendEmail(ctx, to, subject, body); err != nil {
		return err
	}
	s.Outbox.record(to, subject, body)
	return nil
}
