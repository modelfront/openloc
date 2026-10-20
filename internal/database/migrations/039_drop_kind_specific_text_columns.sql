-- +goose Up
-- Drop the kind-specific text columns now that
-- active_text / active_rich_text are the canonical visible target and
-- translation_history audits every text transition.
--
-- Dropped:
--   • mt_text         — MT output. Pre-translated rows store it in active_text;
--                       predict sends active_text to ModelFront; the per-mt
--                       text is preserved in translation_history (kind='mt').
--   • mt_engine       — superseded by `origin` + translation_history.mt_engine_id.
--   • ape_text        — APE rewrite. active_text holds it after an APE outcome;
--                       translation_history (kind='ape') preserves it.
--   • human_text      — human-confirmed target. active_text holds it; the
--                       TM-write / feedback eligibility guard is now
--                       `status='approved'` (human-confirmed).
--   • human_rich_text — rich form of human_text → active_rich_text.
--
-- Guard semantics (auto-approved segments never reach the TM):
--   was `human_text IS NOT NULL`, now `status = 'approved'`. auto_approved
--   (machine-confirmed QP+APE) stays out of the TM. A human edit flips
--   auto_approved → approved via UpdateTranslationHuman.
--
-- Forward-only: restoring these columns + their data requires replaying
-- translation_history, which is impractical in a migration. Rollback path
-- is "restore from backup." The Down section re-creates empty columns only
-- to keep a goose-down chain parseable.

ALTER TABLE translations
    DROP COLUMN mt_text,
    DROP COLUMN mt_engine,
    DROP COLUMN ape_text,
    DROP COLUMN human_text,
    DROP COLUMN human_rich_text;

-- +goose Down
ALTER TABLE translations
    ADD COLUMN mt_text         text,
    ADD COLUMN mt_engine       text,
    ADD COLUMN ape_text        text,
    ADD COLUMN human_text      text,
    ADD COLUMN human_rich_text text;
