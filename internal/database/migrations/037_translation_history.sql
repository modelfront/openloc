-- +goose Up
-- Translation history + active-source denormalization.
--
-- Phase 1 of an 8-phase refactor. This migration is additive in spirit
-- (we do rename one column to free a name, but no data is destroyed and
-- behavior is unchanged after the migration applies + the paired SQL/Go
-- sweep lands in the same PR).
--
-- Touches:
--   - 3 new enum types (translation_history_kind, translation_origin_v2,
--     translation_phase)
--   - translations.origin → translations.legacy_origin (frees the name
--     for the new enum; legacy_origin is dropped in Phase 5)
--   - new columns on translations: origin (new type), tm_match_percent,
--     in_flight_phase
--   - new table translation_history (append-only audit, FK to translations
--     with cascade)
--   - backfill of origin + tm_match_percent from legacy_origin / mt_engine
--     / tm_match_score so existing rows light up the new columns
--     immediately
--
-- The new `translation_origin_v2` type carries only automation sources
-- (`tm`, `mt`, `ape`, `file_import`). `human` and `propagation` are not
-- values — the new semantic is that human action updates `active_text` /
-- `edited_by` / `editing_time_ms` but leaves `origin` and
-- `tm_match_percent` untouched (sticky through human edits).
-- The new `origin` column thus describes "what is the latest pipeline
-- lineage of this row" rather than "who last wrote to it" — answering the
-- latter is done via `edited_by IS NOT NULL`.
--
-- In Phase 5 (separate migration) `legacy_origin` is dropped and the new
-- `translation_origin_v2` type is renamed to `translation_origin`. We
-- live with the `_v2` suffix in between.

-- ---- new enum types ----

CREATE TYPE translation_history_kind AS ENUM (
    'tm', 'mt', 'ape', 'human', 'propagation', 'file_import', 'cleared'
);

CREATE TYPE translation_origin_v2 AS ENUM (
    'tm', 'mt', 'ape', 'file_import'
);

CREATE TYPE translation_phase AS ENUM (
    'tm', 'mt', 'predict'
);

-- ---- rename legacy origin to free the name ----

ALTER TABLE translations RENAME COLUMN origin TO legacy_origin;

-- ---- new translations columns ----

ALTER TABLE translations
    ADD COLUMN origin           translation_origin_v2,
    ADD COLUMN tm_match_percent smallint
        CHECK (tm_match_percent IS NULL OR tm_match_percent BETWEEN 0 AND 101),
    ADD COLUMN in_flight_phase  translation_phase;

-- ---- backfill new columns from legacy state ----
-- Strategy:
--   • Rows with a real automation origin (tm_exact/tm_fuzzy/mt/file_import)
--     map directly.
--   • Rows where legacy_origin was overwritten to 'human' or 'propagation'
--     still carry pipeline lineage via mt_engine. Inferring from mt_engine:
--       mt_engine = 'tm'                → origin = 'tm'
--       mt_engine = 'file_import'       → origin = 'file_import'
--       mt_engine IS NOT NULL otherwise → origin = 'mt'   (real MT engine name)
--       mt_engine IS NULL               → origin = NULL  (pure human translation)
--   • ape rows: legacy schema doesn't carry a distinct origin for APE — the
--     `ape_text` column being non-null is the historical signal. We do NOT
--     backfill `ape` here; rows that had APE rewrites stay as their pre-APE
--     lineage (`mt` typically). Phase 2 emission will start producing `ape`
--     rows correctly going forward.

UPDATE translations SET
    origin = CASE
        WHEN legacy_origin IN ('tm_exact', 'tm_fuzzy') THEN 'tm'::translation_origin_v2
        WHEN legacy_origin = 'mt'                       THEN 'mt'::translation_origin_v2
        WHEN legacy_origin = 'file_import'              THEN 'file_import'::translation_origin_v2
        WHEN legacy_origin IN ('human', 'propagation') THEN
            CASE
                WHEN mt_engine = 'tm'                                                 THEN 'tm'::translation_origin_v2
                WHEN mt_engine = 'file_import'                                        THEN 'file_import'::translation_origin_v2
                WHEN mt_engine IS NOT NULL AND mt_engine NOT IN ('tm', 'file_import') THEN 'mt'::translation_origin_v2
                ELSE NULL
            END
        ELSE NULL
    END,
    tm_match_percent = CASE
        WHEN mt_engine = 'tm' AND tm_match_score IS NOT NULL
            THEN LEAST(100, GREATEST(0, ROUND(tm_match_score * 100)))::smallint
        ELSE NULL
    END;

-- ---- translation_history child table ----

CREATE TABLE translation_history (
    id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    translation_id          bigint NOT NULL REFERENCES translations(id) ON DELETE CASCADE,
    kind                    translation_history_kind NOT NULL,
    -- target text snapshot. NULL only for kind='cleared'.
    text                    text,
    rich_text               text,
    -- who. NULL means a system action with no user attribution. For
    -- pipeline events fired by upload-auto, this is the uploader; for
    -- manual Pre-translate / panel-insert / confirm, the clicking user.
    created_by              bigint REFERENCES users(id) ON DELETE SET NULL,
    -- provenance, sparse. Each FK uses ON DELETE SET NULL so deleting a
    -- TM / MT engine / user / step assignment doesn't nuke the audit.
    tm_id                   bigint REFERENCES tm_collections(id) ON DELETE SET NULL,
    tm_entry_id             bigint REFERENCES translation_memory(id) ON DELETE SET NULL,
    mt_engine_id            bigint REFERENCES mt_engines(id) ON DELETE SET NULL,
    tm_match_percent        smallint
        CHECK (tm_match_percent IS NULL OR tm_match_percent BETWEEN 0 AND 101),
    edit_distance           int,
    job_step_assignment_id  bigint REFERENCES job_step_assignments(id) ON DELETE SET NULL,
    -- evolving unstructured payload. Known keys:
    --   predict_outcome   (string)    -- on kind='ape' or kind='mt'
    --   source            (string)    -- 'pipeline'|'panel_insert' on kind='tm'
    --   source_translation_id (int)   -- on kind='propagation'
    --   error_message     (string)    -- on failed-then-retried writes
    details                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at              timestamptz NOT NULL DEFAULT now()
);

-- "Show me the history of this translation, newest first." Dominant
-- access pattern for the segment history viewer (Phase 7).
CREATE INDEX idx_translation_history_tx_time
    ON translation_history (translation_id, created_at DESC);

-- Per-user productivity queries (Phase 6). Partial index because
-- pipeline-source events have created_by=NULL most of the time and
-- that's not a useful filter target.
CREATE INDEX idx_translation_history_created_by
    ON translation_history (created_by, created_at DESC)
    WHERE created_by IS NOT NULL;

-- Pipeline-event audits ("how many TM matches did we record this week").
CREATE INDEX idx_translation_history_kind
    ON translation_history (kind, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS translation_history;

ALTER TABLE translations
    DROP COLUMN IF EXISTS in_flight_phase,
    DROP COLUMN IF EXISTS tm_match_percent,
    DROP COLUMN IF EXISTS origin;

ALTER TABLE translations RENAME COLUMN legacy_origin TO origin;

DROP TYPE IF EXISTS translation_phase;
DROP TYPE IF EXISTS translation_origin_v2;
DROP TYPE IF EXISTS translation_history_kind;
