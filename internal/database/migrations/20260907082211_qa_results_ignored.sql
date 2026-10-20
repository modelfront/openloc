-- +goose Up
-- Per-finding "ignore" state lives on the qa_results row itself. A QA run
-- upserts rows in place and prunes only the checks that stopped firing, so the
-- flag survives re-runs as long as the finding (check_type + message) is
-- unchanged. A changed message clears it — see UpsertQAResult.
ALTER TABLE qa_results
    ADD COLUMN ignored       boolean NOT NULL DEFAULT false,
    ADD COLUMN ignored_by    bigint REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN ignored_at    timestamptz;

-- +goose Down
ALTER TABLE qa_results
    DROP COLUMN IF EXISTS ignored_at,
    DROP COLUMN IF EXISTS ignored_by,
    DROP COLUMN IF EXISTS ignored;
