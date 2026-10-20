-- Chat. Every query is tenant-scoped: conversations carry tenant_id
-- directly; messages join through their conversation.

-- name: CreateChatConversation :one
INSERT INTO chat_conversations (tenant_id, user_id, title)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetChatConversation :one
SELECT * FROM chat_conversations
WHERE id = $1 AND tenant_id = $2 AND user_id = $3;

-- name: ListChatConversations :many
SELECT * FROM chat_conversations
WHERE tenant_id = $1 AND user_id = $2
ORDER BY updated_at DESC
LIMIT 50;

-- name: TouchChatConversation :exec
UPDATE chat_conversations SET updated_at = now()
WHERE id = $1 AND tenant_id = $2;

-- name: DeleteChatConversation :execrows
DELETE FROM chat_conversations
WHERE id = $1 AND tenant_id = $2 AND user_id = $3;

-- name: InsertChatMessage :one
-- Caller must have verified conversation ownership via GetChatConversation:
-- the insert itself is scoped through the conversation FK.
INSERT INTO chat_messages (conversation_id, role, content, tool_calls, input_tokens, output_tokens)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListChatMessages :many
SELECT m.* FROM chat_messages m
JOIN chat_conversations c ON c.id = m.conversation_id
WHERE m.conversation_id = $1 AND c.tenant_id = $2 AND c.user_id = $3
ORDER BY m.id;

-- name: InsertChatUpload :one
INSERT INTO chat_uploads (tenant_id, user_id, filename, size_bytes, storage_key)
VALUES (@tenant_id, @user_id, @filename, @size_bytes, @storage_key)
RETURNING *;

-- name: GetChatUpload :one
-- Owner-only and unexpired: an upload id is useless to anyone else.
SELECT * FROM chat_uploads
WHERE id = @id AND tenant_id = @tenant_id AND user_id = @user_id AND expires_at > now();

-- Tour picks: a saved tour step that names a real item
-- ("the job with the most segments") resolves to one of these, per tenant.

-- name: PickTourJob :one
-- The job with the most segments in an in-progress project.
SELECT j.id AS job_id, f.project_id
FROM jobs j
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE p.tenant_id = @tenant_id AND p.deleted_at IS NULL AND p.status = 'in_progress'
ORDER BY COALESCE(j.total_count, 0) DESC, j.id DESC
LIMIT 1;

-- name: PickTourPrompt :one
-- A system prompt of one purpose: 'mt' prompts write the translation and set
-- no status or comments (aiprompt.ModeForFlags); every other mode is AI
-- review. The organization's own prompts come before the built-ins, and among
-- them the one with language rules, since the tour points at them.
SELECT p.id FROM ai_prompts p
WHERE p.tenant_id = @tenant_id
  AND (NOT p.out_status AND NOT p.out_comments) = (@purpose::text = 'mt')
ORDER BY p.is_default ASC,
         EXISTS (SELECT 1 FROM ai_prompt_language_additions a WHERE a.prompt_id = p.id) DESC,
         p.updated_at DESC, p.id DESC
LIMIT 1;

-- name: PickTourDashboard :one
-- The shared dashboard with the most charts, and its first chart.
SELECT d.id AS dashboard_id, c.id AS chart_id
FROM dashboards d
JOIN LATERAL (
  SELECT dc.id FROM dashboard_charts dc
  WHERE dc.dashboard_id = d.id AND dc.tenant_id = d.tenant_id AND dc.deleted_at IS NULL
  ORDER BY dc.position, dc.id
  LIMIT 1
) c ON true
WHERE d.tenant_id = @tenant_id AND d.owner_id IS NULL AND d.deleted_at IS NULL
ORDER BY (SELECT count(*) FROM dashboard_charts dc
          WHERE dc.dashboard_id = d.id AND dc.tenant_id = d.tenant_id AND dc.deleted_at IS NULL) DESC,
         d.position, d.id
LIMIT 1;

-- Assistant upload purge. A platform sweep, so these are
-- not tenant-scoped; the chat_uploads delete trigger queues each blob key.

-- name: DeleteExpiredChatUploads :execrows
-- Internal: platform sweep across tenants. Deletes one batch of expired rows.
DELETE FROM chat_uploads
WHERE id IN (
    SELECT id FROM chat_uploads
    WHERE expires_at <= now()
    ORDER BY expires_at
    LIMIT @batch_size
);

-- name: ListChatUploadBlobPurges :many
-- Internal: platform sweep across tenants. One batch of queued blob keys.
SELECT storage_key FROM chat_upload_blob_purges
ORDER BY queued_at, storage_key
LIMIT @batch_size;

-- name: DeleteChatUploadBlobPurges :exec
-- Internal: platform sweep across tenants. Called after the blobs are gone.
DELETE FROM chat_upload_blob_purges WHERE storage_key = ANY(@storage_keys::text[]);
