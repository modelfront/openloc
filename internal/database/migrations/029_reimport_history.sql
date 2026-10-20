-- +goose Up
-- Audit log for XLIFF re-import operations. Re-import is admin-gated and rare,
-- but high-stakes — every run mutates many translation rows in a single tx.
-- Without this table, "did anyone re-import this file last week?" requires
-- grepping server logs. This is the answer for that question.
--
-- Per-segment translation history (every UPDATE to translations.*) is a
-- larger cross-cutting feature; this table is intentionally re-import-specific
-- so partial coverage doesn't pretend to be a full audit log.
CREATE TABLE reimport_history (
    id bigserial PRIMARY KEY,
    file_id bigint NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    -- triggered_by is nullable so user soft-deletes don't cascade-delete history.
    triggered_by bigint REFERENCES users(id) ON DELETE SET NULL,
    target_language text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    force boolean NOT NULL DEFAULT false,
    fallback_to_source boolean NOT NULL DEFAULT false,
    -- Conflict-report counts mirrored 1:1 from ReimportXLIFFResult.
    updated_count int NOT NULL DEFAULT 0,
    skipped_locked_count int NOT NULL DEFAULT 0,
    skipped_approved_count int NOT NULL DEFAULT 0,
    untouched_no_change_count int NOT NULL DEFAULT 0,
    extra_in_upload_count int NOT NULL DEFAULT 0,
    missing_from_upload_count int NOT NULL DEFAULT 0,
    triggered_at timestamptz NOT NULL DEFAULT now()
);

-- File-detail page queries by (file_id, triggered_at DESC) to show the
-- newest-first list. Index supports that access pattern directly.
CREATE INDEX idx_reimport_history_file_recent
    ON reimport_history(file_id, triggered_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_reimport_history_file_recent;
DROP TABLE IF EXISTS reimport_history;
