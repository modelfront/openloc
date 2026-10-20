-- +goose Up
-- OAuth 2.1 authorization server: lets Claude / ChatGPT
-- connect to /mcp on a user's behalf with a consent screen instead of a
-- pasted PAT. All tokens are stored as SHA-256 hashes (same rationale as
-- api_tokens: 256-bit random values need no slow hash).

-- Registered OAuth clients. Two kinds:
--   dcr  — created via RFC 7591 dynamic registration (id is our random id)
--   cimd — client_id IS an https URL to a Client ID Metadata Document; the
--          row caches the fetched metadata (refreshed on use when stale)
CREATE TABLE oauth_clients (
    id            text PRIMARY KEY,
    kind          text NOT NULL CHECK (kind IN ('dcr', 'cimd')),
    name          text NOT NULL DEFAULT '',
    -- Exact-match redirect URIs, except loopback (http://localhost or
    -- http://127.0.0.1) where the port is ignored per RFC 8252 §7.3.
    redirect_uris jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- CIMD refresh bookkeeping; NULL for DCR clients.
    metadata_fetched_at timestamptz
);

-- Single-use authorization codes. Short-lived; deleted on redemption, so a
-- replayed code simply fails as unknown/used. (Family revocation is the
-- refresh-token reuse response, not this path — see token.go.)
CREATE TABLE oauth_auth_codes (
    code_hash      text PRIMARY KEY,
    client_id      text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id        bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id      bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope          text NOT NULL CHECK (scope IN ('read', 'write')),
    redirect_uri   text NOT NULL,
    code_challenge text NOT NULL,  -- PKCE S256 challenge, base64url
    resource       text NOT NULL DEFAULT '',
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- Issued token pairs. family_id groups a refresh chain: rotation inserts a
-- new row in the same family and revokes the old; a REUSED (already-revoked)
-- refresh token revokes the whole family (OAuth 2.1 token-theft response).
CREATE TABLE oauth_tokens (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    family_id          uuid NOT NULL,
    client_id          text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id            bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id          bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope              text NOT NULL CHECK (scope IN ('read', 'write')),
    access_token_hash  text NOT NULL UNIQUE,
    refresh_token_hash text NOT NULL UNIQUE,
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    revoked_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_used_at       timestamptz
);

CREATE INDEX idx_oauth_tokens_family ON oauth_tokens (family_id);
CREATE INDEX idx_oauth_tokens_user ON oauth_tokens (user_id, tenant_id);
-- Expiry sweeps.
CREATE INDEX idx_oauth_auth_codes_expiry ON oauth_auth_codes (expires_at);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_auth_codes;
DROP TABLE oauth_clients;
