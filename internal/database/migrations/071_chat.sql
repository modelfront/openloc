-- +goose Up
-- Built-in AI chat. Conversations are tenant+user scoped; messages
-- replay to the model as text-only history, so content is the only thing that
-- must survive. tool_calls is a display summary (name/outcome), never replayed.

CREATE TABLE chat_conversations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_conversations_tenant_user
    ON chat_conversations (tenant_id, user_id, updated_at DESC);

CREATE TABLE chat_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    -- [{"name": "openloc_list_jobs", "ok": true}, ...] — UI chips only.
    tool_calls JSONB,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_messages_conversation
    ON chat_messages (conversation_id, id);

-- +goose Down
DROP TABLE chat_messages;
DROP TABLE chat_conversations;
