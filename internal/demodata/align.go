package demodata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is what a pool and a transaction have in common.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AlignTranslationDates moves each of a tenant's translations back to its
// first history event when that event is older. Demo data backdates history so
// the charts have curves; without this the translations themselves stay dated
// "now", and source volume (dated by translation) lands in a different week
// from the activity on the same segments.
func AlignTranslationDates(ctx context.Context, x Execer, tenantID int64) error {
	_, err := x.Exec(ctx, `
		UPDATE translations t SET created_at = h.first_at
		FROM (
			SELECT th.translation_id, MIN(th.created_at) AS first_at
			FROM translation_history th
			JOIN translations t2 ON t2.id = th.translation_id
			JOIN segments s ON s.id = t2.segment_id
			JOIN files f ON f.id = s.file_id
			JOIN projects p ON p.id = f.project_id
			WHERE p.tenant_id = $1
			GROUP BY th.translation_id
		) h
		WHERE t.id = h.translation_id AND h.first_at < t.created_at`, tenantID)
	if err != nil {
		return fmt.Errorf("align translation dates: %w", err)
	}
	return nil
}

// HealOriginHistory gives each of a tenant's translations that has an origin
// but no history the event real pre-translation writes for it: kind = origin,
// the current text, dated with the translation, and for MT the tenant's first
// MT engine. Seed rows inserted directly otherwise show in source volume
// (dated by translation) but not in MT usage (counted from history).
// Idempotent: a translation with any history is left alone.
func HealOriginHistory(ctx context.Context, x Execer, tenantID int64) error {
	_, err := x.Exec(ctx, `
		INSERT INTO translation_history (translation_id, kind, text, rich_text, mt_engine_id, details, created_at)
		SELECT t.id, t.origin::text::translation_history_kind, t.active_text, t.active_rich_text,
		       CASE WHEN t.origin = 'mt' THEN me.id END,
		       CASE WHEN t.origin = 'mt' AND me.id IS NOT NULL THEN jsonb_build_object('engine_name', me.name) ELSE '{}'::jsonb END,
		       t.created_at
		FROM translations t
		JOIN segments s ON s.id = t.segment_id
		JOIN files f ON f.id = s.file_id
		JOIN projects p ON p.id = f.project_id
		LEFT JOIN LATERAL (
			SELECT id, name FROM mt_engines WHERE tenant_id = p.tenant_id ORDER BY id LIMIT 1
		) me ON true
		WHERE p.tenant_id = $1
		  AND t.origin IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM translation_history h WHERE h.translation_id = t.id)`, tenantID)
	if err != nil {
		return fmt.Errorf("heal origin history: %w", err)
	}
	return nil
}
