package ai

import (
	"context"
	"strings"
	"sync"
	"time"

	"openloc/internal/logging"

	db "openloc/internal/database/sqlcgen"
)

// Keeping the per-tenant model catalog warm: on each tick, one pass over the
// pairs already in the cache.

// refreshConcurrency bounds simultaneous provider calls in one pass. The
// refresh is background work — it must not look like a burst to a vendor's rate
// limiter, and finishing in seconds instead of milliseconds costs nothing.
const refreshConcurrency = 4

// refreshTimeout bounds one full pass, so a hung provider can't leave the
// goroutine wedged until the next tick.
const refreshTimeout = 2 * time.Minute

// DefaultIdleTTL is how long an unread entry survives. Past it the pair is
// dropped and stops costing a refresh; the next read fills it again. Long
// enough that an org using the app daily never re-pays the on-demand fetch.
const DefaultIdleTTL = 24 * time.Hour

// Decryptor decrypts a stored provider key (implemented by *secrets.Encryptor).
type Decryptor interface {
	Decrypt(encoded string) (string, error)
}

// DBKeyLoader resolves a tenant's vendor credentials from the database. It is
// the catalog's only route to SQL: both the on-demand fetch and the periodic
// refresh reach the database through here and nowhere else.
type DBKeyLoader struct {
	q   db.Querier
	enc Decryptor
}

func NewDBKeyLoader(q db.Querier, enc Decryptor) *DBKeyLoader {
	return &DBKeyLoader{q: q, enc: enc}
}

// KeysFor returns every key the tenant holds for provider, best first — the one
// the built-in chat spends, then oldest — so the catalog reflects the credential
// that will actually be used, and can fall back when it doesn't work.
//
// A key that won't decrypt is skipped rather than fatal: AI_SECRETS_KEY was
// rotated for that row, and the tenant's other keys may still be readable.
func (l *DBKeyLoader) KeysFor(ctx context.Context, tenantID int64, provider string) ([]Credential, error) {
	rows, err := l.q.ListVendorTokenKeys(ctx, db.ListVendorTokenKeysParams{
		TenantID: tenantID,
		Provider: provider,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// No rows is the ordinary "vendor not connected" state, not a failure.
		return nil, ErrNoCredential
	}
	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		key, err := l.enc.Decrypt(row.ApiKeyEncrypted)
		if err != nil {
			logging.Warn(ctx, err, "skipping a key that will not decrypt", "ai-model-key-undecryptable",
				logging.TenantID(tenantID), logging.Provider(provider))
			continue
		}
		out = append(out, Credential{TokenID: row.ID, APIKey: key, BaseURL: strings.TrimSpace(row.BaseUrl)})
	}
	if len(out) == 0 {
		return nil, ErrNoCredential
	}
	return out, nil
}

// Refresher keeps the WARM part of the catalog fresh: it re-reads the pairs
// somebody is actually using and drops the ones nobody has touched.
//
// It deliberately does not sweep the database for every tenant with a key. The
// catalog fills on demand, so an entry exists only because someone opened a
// surface that needed it — which makes the cache itself the work list, and makes
// refresh cost track ACTIVE organizations instead of all of them. An install
// with 5,000 orgs where 40 use AI settings this week does 80 calls a pass, not
// 10,000.
type Refresher struct {
	catalog  *Catalog
	interval time.Duration
	idleTTL  time.Duration
}

// NewRefresher builds the periodic refresher. interval <= 0 uses the default;
// idleTTL <= 0 uses DefaultIdleTTL.
func NewRefresher(catalog *Catalog, interval, idleTTL time.Duration) *Refresher {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	return &Refresher{catalog: catalog, interval: interval, idleTTL: idleTTL}
}

// Run refreshes warm entries on the interval until ctx is done. Call it in a
// goroutine.
//
// There is no startup warm: with an empty cache there is nothing to keep warm,
// and the first read of any pair fills it.
//
// An in-process ticker rather than an asynq periodic task: the catalog is this
// process's memory, so a task claimed by one replica would leave every other
// replica stale. For the same reason it is not gated on RUN_SCHEDULER, which
// exists to keep N replicas from sending N copies of the same email.
func (r *Refresher) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RefreshAll(ctx)
		}
	}
}

// RefreshAll re-reads every warm pair and evicts the idle ones. A pair that
// fails keeps its previous entry — a vendor blip must not empty a working
// dropdown.
func (r *Refresher) RefreshAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()

	keys := r.catalog.warmKeys(r.idleTTL)
	if len(keys) == 0 {
		return
	}
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, refreshConcurrency)
		ok  int
		mu  sync.Mutex
	)
	for _, key := range keys {
		wg.Add(1)
		go func(k catalogKey) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := r.catalog.Refresh(ctx, k.tenantID, k.provider); err != nil {
				// The error may quote the provider's response body, which is why
				// this is a log and not an errtrack notice.
				logging.Warn(ctx, err, "model catalog refresh failed", "ai-model-refresh-failed",
					logging.TenantID(k.tenantID), logging.Provider(k.provider))
				return
			}
			mu.Lock()
			ok++
			mu.Unlock()
		}(key)
	}
	wg.Wait()
	logging.Info(ctx, "model catalog refreshed", "ai-model-refresh",
		logging.Debugf("pairs_ok", ok), logging.Debugf("pairs_warm", len(keys)))
}
