-- +goose Up
-- Files a user attached in the assistant panel. A tool
-- that imports a file (TMX, terms, a document) takes the upload id; reads are
-- scoped to tenant AND user and ignore expired rows.
CREATE TABLE chat_uploads (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    storage_key  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours'
);
CREATE INDEX chat_uploads_owner_idx ON chat_uploads (tenant_id, user_id, created_at DESC);

-- +goose Down
DROP TABLE chat_uploads;
