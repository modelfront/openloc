-- OAuth 2.1 authorization server. Codes and tokens are
-- stored hashed; every lookup is by hash. Tenant scoping on the connections
-- listing is direct (user_id + tenant_id are columns).

-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (id, kind, name, redirect_uris, metadata_fetched_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
  SET name = EXCLUDED.name,
      redirect_uris = EXCLUDED.redirect_uris,
      metadata_fetched_at = EXCLUDED.metadata_fetched_at
RETURNING id, kind, name, redirect_uris, created_at, metadata_fetched_at;

-- name: GetOAuthClient :one
SELECT id, kind, name, redirect_uris, created_at, metadata_fetched_at
FROM oauth_clients WHERE id = $1;

-- name: CreateOAuthCode :exec
INSERT INTO oauth_auth_codes
  (code_hash, client_id, user_id, tenant_id, scope, redirect_uri, code_challenge, resource, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ConsumeOAuthCode :one
-- Single-use: the DELETE is the redemption; a second redemption finds nothing.
DELETE FROM oauth_auth_codes WHERE code_hash = $1
RETURNING code_hash, client_id, user_id, tenant_id, scope, redirect_uri, code_challenge, resource, expires_at;

-- name: DeleteExpiredOAuthCodes :execrows
-- Internal: hygiene sweep, no tenant scoping needed (codes are opaque hashes).
DELETE FROM oauth_auth_codes WHERE expires_at < now();

-- name: CreateOAuthToken :one
INSERT INTO oauth_tokens
  (family_id, client_id, user_id, tenant_id, scope, access_token_hash, refresh_token_hash, access_expires_at, refresh_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, family_id;

-- name: GetOAuthTokenByAccessHash :one
SELECT t.id, t.family_id, t.client_id, t.user_id, t.tenant_id, t.scope,
       t.access_expires_at, t.refresh_expires_at, t.revoked_at, t.last_used_at,
       c.name AS client_name
FROM oauth_tokens t
JOIN oauth_clients c ON c.id = t.client_id
WHERE t.access_token_hash = $1;

-- name: GetOAuthTokenByRefreshHash :one
SELECT id, family_id, client_id, user_id, tenant_id, scope,
       access_expires_at, refresh_expires_at, revoked_at
FROM oauth_tokens WHERE refresh_token_hash = $1;

-- name: RevokeOAuthToken :exec
UPDATE oauth_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeOAuthTokenFamily :execrows
UPDATE oauth_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL;

-- name: TouchOAuthToken :exec
UPDATE oauth_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListOAuthConnections :many
-- One row per live token family for the Settings "Connected apps" list.
SELECT DISTINCT ON (t.family_id)
       t.family_id, t.client_id, c.name AS client_name, t.scope,
       t.tenant_id, t.created_at, t.last_used_at
FROM oauth_tokens t
JOIN oauth_clients c ON c.id = t.client_id
WHERE t.user_id = $1 AND t.tenant_id = $2
  AND t.revoked_at IS NULL AND t.refresh_expires_at > now()
ORDER BY t.family_id, t.created_at DESC;

-- name: RevokeOAuthConnection :execrows
-- Scoped to the calling user: you can only disconnect your own grants.
UPDATE oauth_tokens SET revoked_at = now()
WHERE family_id = $1 AND user_id = $2 AND tenant_id = $3 AND revoked_at IS NULL;
