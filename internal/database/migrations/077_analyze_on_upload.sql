-- +goose Up
-- Auto-analyze on upload: one coalesced project-scoped analysis.
--
-- Auto-analysis is chosen per upload (a checkbox in the upload UI), not a
-- project/template setting. Rather than one analysis per file, an upload
-- enqueues a single project-level analysis; a partial unique index makes that
-- coalescing automatic — at most one PENDING auto analysis per project — so a
-- multi-file upload (or a burst of uploads) collapses into one analysis that
-- runs once the project's pretranslate settles.
--
--  * analyses.is_auto — marks the coalesced auto-analysis row. Its job set is
--    resolved from the project at run time (so files that join the batch late
--    are included). Manual analyses (is_auto=false) keep their stored job_ids.
--  * uq_pending_auto_analysis — the coalescing key: INSERT ... ON CONFLICT DO
--    NOTHING against it means a second file in the same upload finds the
--    pending row already there and no-ops.

ALTER TABLE analyses
    ADD COLUMN is_auto boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX uq_pending_auto_analysis
    ON analyses (project_id)
    WHERE is_auto AND status = 'pending';

-- +goose Down
DROP INDEX uq_pending_auto_analysis;
ALTER TABLE analyses DROP COLUMN is_auto;
