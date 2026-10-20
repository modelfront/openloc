-- +goose Up
-- Personal access tokens for machine clients.
--
-- These are the credential MCP clients (Claude Code / Claude Desktop / agents)
-- present instead of the browser's 24h JWT. Key properties:
--   * token_hash is SHA-256(raw token) — the plaintext is shown exactly once at
--     creation and never stored. High-entropy random secret, so a fast hash is
--     correct here (this is not a password; no bcrypt work factor needed).
--   * Scoped to ONE (tenant, user) pair. A user in N orgs creates N tokens.
--     This keeps tenant_id out of every MCP tool signature, so no tool can be
--     talked into crossing a tenant boundary.
--   * `role` is deliberately NOT stored — it is resolved per-request from
--     memberships so a demotion or org-removal takes effect immediately.
--   * expires_at is NOT NULL: no immortal tokens.
CREATE TABLE api_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    -- Display-only fragment ("eltm_pat_a1b2c3…") so the UI can identify a token
    -- in a list without ever holding the secret.
    prefix       text NOT NULL,
    capability   text NOT NULL DEFAULT 'read' CHECK (capability IN ('read', 'write')),
    last_used_at timestamptz,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Hot path: resolve a presented token. Partial index keeps revoked rows out.
CREATE INDEX idx_api_tokens_hash_active ON api_tokens (token_hash) WHERE revoked_at IS NULL;
-- Listing surfaces: "my tokens" and "all tokens in this org" (manager view).
CREATE INDEX idx_api_tokens_user_tenant ON api_tokens (user_id, tenant_id);
CREATE INDEX idx_api_tokens_tenant ON api_tokens (tenant_id);

-- Audit trail for MCP tool invocations.
--
-- Every tool call lands here, including denials. Without this, "Claude said it
-- couldn't find my project" is undebuggable, and once write tools ship there is
-- no way to answer "which agent changed this segment".
--
-- args_digest is a SHA-256 of the canonical JSON arguments, NOT the arguments
-- themselves — tool args can contain source content, and this table must not
-- become a second copy of customer text.
--
-- user_id / token_id are ON DELETE SET NULL so revoking a token or removing a
-- user never erases the audit history of what they did.
CREATE TABLE mcp_tool_calls (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     bigint REFERENCES users(id) ON DELETE SET NULL,
    token_id    bigint REFERENCES api_tokens(id) ON DELETE SET NULL,
    tool        text NOT NULL,
    args_digest text NOT NULL DEFAULT '',
    outcome     text NOT NULL CHECK (outcome IN ('ok', 'error', 'denied')),
    error_code  text,
    duration_ms integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_mcp_tool_calls_tenant_created ON mcp_tool_calls (tenant_id, created_at DESC);

-- +goose Down
DROP TABLE mcp_tool_calls;
DROP TABLE api_tokens;
