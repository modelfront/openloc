-- ── AI vendors (OpenAI / Claude / extensions) ──

-- name: ListAIVendors :many
SELECT id, tenant_id, provider, created_at, updated_at
FROM ai_vendors
WHERE tenant_id = $1
ORDER BY provider;

-- name: GetAIVendor :one
SELECT * FROM ai_vendors
WHERE tenant_id = $1 AND provider = $2;

-- name: UpsertAIVendor :one
-- Ensure a vendor row exists for (tenant, provider); returns it. Creating a
-- token calls this first so the vendor is present.
INSERT INTO ai_vendors (tenant_id, provider)
VALUES ($1, $2)
ON CONFLICT (tenant_id, provider) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: DeleteAIVendor :exec
DELETE FROM ai_vendors WHERE tenant_id = $1 AND provider = $2;

-- ── Vendor tokens (many API keys per vendor) ──

-- name: ListVendorTokens :many
-- All tokens for a tenant's vendors, excluding the ciphertext (never surfaced).
-- key_last4 is a non-secret hint shown in the edit UI.
SELECT t.id, t.vendor_id, t.connection_id, v.provider, t.name, c.base_url, t.key_last4, t.use_for_chat, t.created_at, t.updated_at
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
JOIN ai_connections c ON c.id = t.connection_id
WHERE v.tenant_id = $1
ORDER BY v.provider, t.created_at, t.id;

-- name: GetVendorToken :one
-- Full token row incl. ciphertext + its vendor's provider + tenant (internal /
-- runtime use). Tenant-scoped via the vendor join.
-- base_url comes from the token's CONNECTION; the column
-- name is kept so runtime callers reading BaseUrl needed no change.
SELECT t.*, v.provider AS provider, v.tenant_id AS tenant_id, c.base_url AS base_url
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
JOIN ai_connections c ON c.id = t.connection_id
WHERE t.id = $1 AND v.tenant_id = $2;

-- name: GetFirstVendorToken :one
-- Oldest token for a (tenant, provider) vendor — used by seed and demo data.
-- Includes ciphertext.
SELECT t.*, c.base_url AS base_url
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
JOIN ai_connections c ON c.id = t.connection_id
WHERE v.tenant_id = $1 AND v.provider = $2
ORDER BY t.created_at, t.id
LIMIT 1;

-- name: CreateVendorToken :one
INSERT INTO ai_vendor_tokens (vendor_id, name, api_key_encrypted, connection_id, key_last4)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateVendorToken :one
UPDATE ai_vendor_tokens
SET name = $3, api_key_encrypted = $4, connection_id = $5, key_last4 = $6, updated_at = now()
WHERE id = $1 AND vendor_id = $2
RETURNING *;

-- name: DeleteVendorToken :exec
DELETE FROM ai_vendor_tokens WHERE id = $1 AND vendor_id = $2;

-- ── AI connections (vendor + endpoint; tokens attach to one) ──

-- name: ListAIConnections :many
-- Every connection for a tenant, with its vendor and a live token count so the
-- UI can show "configured but no key yet" without a second round trip.
SELECT c.id, c.vendor_id, v.provider, c.name, c.base_url, c.created_at, c.updated_at,
       (SELECT count(*)::int FROM ai_vendor_tokens t WHERE t.connection_id = c.id) AS token_count
FROM ai_connections c
JOIN ai_vendors v ON v.id = c.vendor_id
WHERE v.tenant_id = $1
ORDER BY v.provider, c.created_at, c.id;

-- name: GetAIConnection :one
SELECT c.*, v.provider AS provider, v.tenant_id AS tenant_id
FROM ai_connections c
JOIN ai_vendors v ON v.id = c.vendor_id
WHERE c.id = $1 AND v.tenant_id = $2;

-- name: CreateAIConnection :one
INSERT INTO ai_connections (vendor_id, name, base_url)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateAIConnection :one
UPDATE ai_connections
SET name = $3, base_url = $4, updated_at = now()
WHERE id = $1 AND vendor_id = $2
RETURNING *;

-- name: DeleteAIConnection :exec
DELETE FROM ai_connections WHERE id = $1 AND vendor_id = $2;

-- name: CountTokensForConnection :one
-- Delete guard: tokens FK to the connection ON DELETE RESTRICT, so a clean 409
-- beats an opaque 500.
SELECT count(*)::int AS count FROM ai_vendor_tokens WHERE connection_id = $1;

-- name: GetChatVendorToken :one
-- The key the built-in chat should use for a vendor.
--
-- Prefers the one explicitly marked `use_for_chat`, and falls back to the
-- oldest — the historical behaviour — so a tenant that never chose still gets
-- the same key as before. Separate from GetFirstVendorToken because that one
-- also serves other callers, and "which key the chat spends" must not
-- silently repoint them.
SELECT t.*, c.base_url AS base_url
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
JOIN ai_connections c ON c.id = t.connection_id
WHERE v.tenant_id = $1 AND v.provider = $2
ORDER BY t.use_for_chat DESC, t.created_at, t.id
LIMIT 1;

-- name: ListVendorTokenKeys :many
-- Internal: includes ciphertext. Every key for one (tenant, provider), in the
-- order the runtime prefers them: the explicit chat key, then oldest. The model
-- catalog tries them in that order and stops at the first that answers, so one
-- dead key can't hide a vendor the tenant can reach.
SELECT t.id, t.api_key_encrypted, c.base_url
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
JOIN ai_connections c ON c.id = t.connection_id
WHERE v.tenant_id = $1 AND v.provider = $2
ORDER BY t.use_for_chat DESC, t.created_at, t.id;

-- name: SetChatVendorToken :exec
-- Make exactly one token the chat key for its vendor. Written as a single
-- statement over the whole vendor so there is no window with two winners and
-- none with zero — every other row is set false in the same pass.
UPDATE ai_vendor_tokens
SET use_for_chat = (id = $1), updated_at = now()
WHERE vendor_id = $2;
