-- +goose Up
-- A step round that reviews every translated, unlocked segment with its AI
-- router, people's text included: set when a rollback asks for the AI to run
-- again, cleared when the next round opens.
ALTER TABLE job_step_assignments ADD COLUMN ai_review_all BOOLEAN NOT NULL DEFAULT false;

-- A QE score is shown only while it describes the segment's text. The AI only
-- scores segments no person has edited, so a score on an edited segment is
-- about text that is gone.
UPDATE translations SET qe_note = NULL WHERE qe_note IS NOT NULL AND edited_by IS NOT NULL;

-- +goose Down
-- The cleared scores stay in translation_history details; they are not put back.
ALTER TABLE job_step_assignments DROP COLUMN ai_review_all;
