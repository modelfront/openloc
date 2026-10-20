-- +goose Up
-- Password reset tokens. token_hash is SHA-256 hex of the raw token
-- (PAT pattern): the plaintext is emailed to the user once and never
-- stored. Single-use (used_at) with a 1h expiry; a new request deletes the
-- user's previous tokens.
CREATE TABLE password_reset_tokens (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens (user_id);

-- +goose Down
DROP TABLE IF EXISTS password_reset_tokens;
