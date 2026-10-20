-- +goose Up
-- +goose StatementBegin

-- Per-entry history for translation_memory edits. Every PATCH on a TM entry
-- appends a row here BEFORE the update, in the same transaction, capturing
-- a full snapshot of the editable fields as they were before the change.
--
-- Why a separate history table (not just `updated_at` + `updated_by`):
--   1. Auditability — answers "what did this entry look like 3 weeks ago,
--      who changed it, was it bulk or individual."
--   2. Recovery — `tm-editing.md` plan lists this as a complement to
--      optimistic locking: the 409 prevents most concurrent overwrites,
--      but for the legitimate sequential-edit case where someone needs
--      the previous value, history is the source of truth.
--   3. Forward-compatible with the future "view history" UI surface
--      (out of scope for the inline-edit slice; this table is the
--      precondition for that work).
--
-- Snapshot stores values BEFORE the change so the row chain reads
-- "newest = current `translation_memory` row, then older versions = each
-- history row in descending `created_at` order."
--
-- `changed_by_user_id` is `int8 NULL` so background processes / pipeline
-- writes can record history without forcing a real user. The frontend
-- inline edit ALWAYS passes the editing user.
--
-- Indexes:
--   - (entry_id, created_at DESC) for "show me the history of this entry"
--   - tenant scoping is implicit via JOIN on translation_memory → tm_collections,
--     no separate tenant_id column needed (parent always carries it).

CREATE TABLE translation_memory_history (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_id          bigint NOT NULL REFERENCES translation_memory(id) ON DELETE CASCADE,
    -- Snapshot of the fields as they were BEFORE the change that produced
    -- this history row. Kept as plain columns (not JSONB) for cheap
    -- indexability + clarity — the editable surface is small (4 fields).
    source_text       text NOT NULL,
    target_text       text NOT NULL,
    source_rich_text  text,
    target_rich_text  text,
    -- Who made the change that overwrote these values. NULL for system /
    -- background writes. Foreign key uses ON DELETE SET NULL so deleting
    -- a user (per j20 soft-delete or hard-delete) doesn't nuke the history.
    changed_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL,
    -- Why the change happened. Free-form short string. Known values:
    --   'edit'             — TM browser inline cell edit (handler.go::HandleUpdateEntry)
    --   'find_replace'     — bulk Find & Replace in TM browser (handler.go::HandleTMFindReplace)
    --   'cat_confirm'      — CAT editor segment confirm via pipeline write-back
    --   'feedback_bulk'    — /feedback API bulk send via pipeline.SendFeedback
    --   'migration_dedupe' — one-shot migration 023 cleanup of pre-existing duplicates
    -- Defaults to '' rather than NULL to keep query filters simple.
    change_reason     text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- "Show me the history of this entry, newest first" — the dominant access
-- pattern for the future history viewer.
CREATE INDEX idx_tm_history_entry_created
    ON translation_memory_history (entry_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS translation_memory_history;

-- +goose StatementEnd
