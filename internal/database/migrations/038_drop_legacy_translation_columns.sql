-- +goose Up
-- Drop the cleanly-unused legacy columns and rename
-- the V2 origin enum back to its natural name.
--
-- Columns dropped:
--   • legacy_origin         — replaced by `origin` (translation_origin_v2). No
--                             reader after Phase 3 / 3.5.
--   • tm_match_score (real) — replaced by `tm_match_percent` (smallint 0..101).
--                             Backfilled in migration 037.
--   • predict_approved (bool) — redundant with predict_outcome='approved'.
--   • ape_approved (bool)     — redundant with predict_outcome IN
--                             ('ape_approved','ape_needs_review').
--
-- NOT dropped in this slice (Phase 5b deferred):
--   • mt_text / mt_engine / ape_text / human_text / human_rich_text — still
--     read by predict.go, tm_write.go, feedback.go, export.go. A separate
--     refactor pivots reads to active_text + translation_history JOINs.
--   • Status enum collapse to 3 values — Phase 5c.
--
-- Type rename: with legacy_origin gone, nothing references the old
-- `translation_origin` enum, so we drop it and rename `translation_origin_v2`
-- back to `translation_origin`. The sqlc-generated `db.TranslationOriginV2*`
-- constants become `db.TranslationOrigin*` after regen — Go callers updated
-- in lockstep.
--
-- Goose `Down` is one-way for this migration: restoring legacy_origin /
-- tm_match_score / predict_approved / ape_approved requires backfilling from
-- history, which is impractical inside a migration. Document this as a
-- forward-only step; rollback strategy is "restore from backup."

ALTER TABLE translations
    DROP COLUMN legacy_origin,
    DROP COLUMN tm_match_score,
    DROP COLUMN predict_approved,
    DROP COLUMN ape_approved;

DROP TYPE translation_origin;
ALTER TYPE translation_origin_v2 RENAME TO translation_origin;

-- +goose Down
-- One-way migration: see header. Re-creating these columns leaves them NULL,
-- which is not a faithful restore but at least keeps the schema parseable for
-- a goose-down chain. Type recreation mirrors the original 001_init shape.

CREATE TYPE translation_origin_old AS ENUM (
    'tm_exact', 'tm_fuzzy', 'mt', 'human', 'file_import', 'propagation'
);

ALTER TYPE translation_origin RENAME TO translation_origin_v2;
ALTER TYPE translation_origin_old RENAME TO translation_origin;

ALTER TABLE translations
    ADD COLUMN legacy_origin     translation_origin,
    ADD COLUMN tm_match_score    real,
    ADD COLUMN predict_approved  boolean,
    ADD COLUMN ape_approved      boolean;
