package aiusage

import (
	"context"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/ptr"
)

// Store writes usage rows straight to Postgres.
//
// Synchronous, unlike llmdebug's buffered channel. One row per provider call is
// a small write next to the call itself, and a queue that drops under load
// would make every total quietly wrong. A failure is logged and swallowed: the
// translation is the work, this is the receipt.
type Store struct{ q db.Querier }

func NewStore(q db.Querier) *Store { return &Store{q: q} }

func (s *Store) Record(ctx context.Context, provider, model string, u Usage) {
	if u.Empty() {
		return
	}
	m := MetaFromContext(ctx)
	if m.TenantID == 0 {
		// No attribution means no tenant to scope the row to, and an unscoped
		// row is one no query may ever read. Louder than a silent drop.
		logging.Warn(ctx, nil, "AI usage reported with no tenant attribution; dropping", "ai-usage-unattributed")
		return
	}
	if err := s.q.InsertAIUsageEvent(ctx, db.InsertAIUsageEventParams{
		TenantID:     m.TenantID,
		AiEngineID:   ptr.NilIfZero(m.AIEngineID),
		AiEngineName: m.AIEngineName,
		Purpose:      m.Purpose,
		Provider:     provider,
		Model:        model,
		ProjectID:    ptr.NilIfZero(m.ProjectID),
		JobID:        ptr.NilIfZero(m.JobID),
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
	}); err != nil {
		logging.Error(ctx, err, "recording AI usage failed", "ai-usage-insert-failed")
	}
}
