package testutil

import (
	"bytes"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/notification"
)

// PNG is a file the image sniffer accepts as a PNG, different for each seed.
func PNG(seed string) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("png "+seed+" "), 16)...)
}

// NewTestRouterWithIssues is the router with issue reporting on: operators are
// the platform admins, emails land in the returned outbox, and the reporter's
// notifications are written for real.
func NewTestRouterWithIssues(pool *pgxpool.Pool, store file.BlobStore, operators []string) (chi.Router, *notification.Outbox) {
	outbox := notification.NewOutbox(100)
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	deps := testDeps(pool, nil, "", TestAICatalog(pool), store)
	deps.PlatformAdminEmails = operators
	deps.EmailSender = sender
	deps.NotificationService = notification.NewService(db.New(pool), sender)
	return api.NewRouter(deps), outbox
}
