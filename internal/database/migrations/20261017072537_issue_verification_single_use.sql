-- +goose Up
-- The id of the token the assistant's check came with. A token proves one
-- check for one report: the unique index refuses it on a second report, also
-- after the first one was deleted.
ALTER TABLE issues
    ADD COLUMN chat_verification_token_id text
        CHECK (char_length(chat_verification_token_id) BETWEEN 1 AND 64),
    ADD CONSTRAINT issues_verification_token_check
        CHECK (chat_verification_token_id IS NULL OR chat_verification IS NOT NULL);

CREATE UNIQUE INDEX issues_verification_token_idx ON issues (chat_verification_token_id)
    WHERE chat_verification_token_id IS NOT NULL;

-- +goose Down
DROP INDEX issues_verification_token_idx;
ALTER TABLE issues
    DROP CONSTRAINT issues_verification_token_check,
    DROP COLUMN chat_verification_token_id;
