-- +goose Up
-- The assistant's check of a report it wrote: whether it could confirm the
-- problem, and one line of what it saw. NULL on a report the assistant did not
-- write. The support team's alone: the reporter's org never reads it.
ALTER TABLE issues
    ADD COLUMN chat_verification text CHECK (chat_verification IN ('verified', 'not_reproduced', 'unverified')),
    ADD COLUMN chat_verification_note text CHECK (char_length(chat_verification_note) BETWEEN 1 AND 300),
    ADD CONSTRAINT issues_chat_verification_pair_check
        CHECK (chat_verification_note IS NULL OR chat_verification IS NOT NULL);

-- What the browser captured with a report, for the support team alone: the
-- action trail (the last pages, clicks and errors before the report) and the
-- recording of the user repeating the steps. Never returned to the reporter's
-- org, and never given to a model. Its own table, so the issue lists never
-- read it.
CREATE TABLE issue_captures (
    issue_id bigint PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    trail jsonb,
    recording jsonb,
    CHECK (trail IS NOT NULL OR recording IS NOT NULL)
);

-- +goose Down
DROP TABLE issue_captures;
ALTER TABLE issues
    DROP CONSTRAINT issues_chat_verification_pair_check,
    DROP COLUMN chat_verification_note,
    DROP COLUMN chat_verification;
