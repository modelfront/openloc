-- +goose Up
-- XLIFF re-imports are recorded as `file.reimported` project events.
DROP TABLE IF EXISTS reimport_history;

-- +goose Down
-- Restores the table empty; the dropped rows do not come back.
CREATE TABLE reimport_history (
    id bigserial PRIMARY KEY,
    file_id bigint NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    triggered_by bigint REFERENCES users(id) ON DELETE SET NULL,
    target_language text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    force boolean NOT NULL DEFAULT false,
    fallback_to_source boolean NOT NULL DEFAULT false,
    updated_count int NOT NULL DEFAULT 0,
    skipped_locked_count int NOT NULL DEFAULT 0,
    skipped_approved_count int NOT NULL DEFAULT 0,
    untouched_no_change_count int NOT NULL DEFAULT 0,
    extra_in_upload_count int NOT NULL DEFAULT 0,
    missing_from_upload_count int NOT NULL DEFAULT 0,
    triggered_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_reimport_history_file_recent
    ON reimport_history(file_id, triggered_at DESC);
