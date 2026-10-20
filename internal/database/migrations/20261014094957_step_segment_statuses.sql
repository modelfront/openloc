-- +goose Up
-- How many times the step has been started or reopened. Each start opens a
-- new round in translation_step_statuses.
ALTER TABLE job_step_assignments ADD COLUMN round INT NOT NULL DEFAULT 0;
UPDATE job_step_assignments SET round = 1 WHERE status <> 'pending';

-- A person changed the text during the job's current step. Reset whenever a
-- step round opens; edited_by stays the lifetime "last human editor".
ALTER TABLE translations ADD COLUMN step_edited BOOLEAN NOT NULL DEFAULT false;
UPDATE translations SET step_edited = true
WHERE status = 'needs_review' AND edited_by IS NOT NULL AND active_text IS NOT NULL;

-- One row per segment per step round: written when the round opens, the end
-- columns filled when it closes (NULL while the step is in progress).
CREATE TABLE translation_step_statuses (
    id                     BIGSERIAL PRIMARY KEY,
    tenant_id              BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    translation_id         BIGINT NOT NULL REFERENCES translations(id) ON DELETE CASCADE,
    job_step_assignment_id BIGINT NOT NULL REFERENCES job_step_assignments(id) ON DELETE CASCADE,
    round                  INT NOT NULL,
    started_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- predict_outcome when the round opened: an outcome that appears by the
    -- end was produced by an AI run inside this round.
    start_predict_outcome  predict_outcome,
    ended_at               TIMESTAMPTZ,
    end_reason             TEXT CHECK (end_reason IN ('completed', 'rejected')),
    status                 translation_status,
    confirmed_via          TEXT,
    predict_outcome        predict_outcome,
    human_edited           BOOLEAN,
    -- md5 of the text at close. A reopened step gets its statuses back only
    -- where the text still matches.
    end_text_md5           TEXT,
    UNIQUE (translation_id, job_step_assignment_id, round)
);

CREATE INDEX idx_translation_step_statuses_assignment
    ON translation_step_statuses (job_step_assignment_id, round);

-- +goose Down
DROP TABLE translation_step_statuses;
ALTER TABLE translations DROP COLUMN step_edited;
ALTER TABLE job_step_assignments DROP COLUMN round;
