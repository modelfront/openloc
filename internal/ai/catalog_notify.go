package ai

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"openloc/internal/logging"
	"openloc/internal/pglisten"
)

// CatalogNotifyChannel is the Postgres channel the ai_vendor_tokens and
// ai_connections triggers notify, with "<tenant id>|<provider>" as the
// payload, when a key or an endpoint changes.
const CatalogNotifyChannel = "ai_catalog_forget"

// ListenOn makes l deliver key changes made on any server process, so every
// process forgets that pair's cached list. Call it before l.Run.
func (c *Catalog) ListenOn(l *pglisten.Listener) {
	l.Handle(CatalogNotifyChannel, catalogReceiver{c})
}

// catalogReceiver is the catalog's pglisten.Handler.
type catalogReceiver struct{ c *Catalog }

// Up forgets everything: changes committed while no connection listened were
// never delivered.
func (r catalogReceiver) Up() { r.c.ForgetAll() }

func (r catalogReceiver) Down() {}

// Notify forgets the pair a notice names.
func (r catalogReceiver) Notify(ctx context.Context, payload string) {
	tenant, provider, ok := strings.Cut(payload, "|")
	tenantID, err := strconv.ParseInt(tenant, 10, 64)
	if !ok || err != nil || provider == "" {
		logging.Warn(ctx, errors.New("not a tenant|provider pair"), "AI catalog notice ignored", "ai-catalog-notice-bad-payload",
			slog.String("payload", payload))
		return
	}
	r.c.Forget(tenantID, provider)
}
