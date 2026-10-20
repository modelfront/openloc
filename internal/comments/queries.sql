-- name: ListCommentsByTranslation :many
-- LEFT JOIN: a NULL user_id is an AI/system comment — the
-- frontend renders those authored "AI".
SELECT sc.id, sc.translation_id, sc.user_id, sc.content, sc.created_at, sc.updated_at,
       u.name AS author_name, u.email AS author_email
FROM segment_comments sc
LEFT JOIN users u ON u.id = sc.user_id
WHERE sc.translation_id = $1 AND sc.deleted_at IS NULL
ORDER BY sc.created_at ASC;

-- name: ListCommentsByTranslationIDs :many
-- Internal: batched comment fetch for the <comments> segment-prompt placeholder.
-- Called only when the tenant's segment prompt uses the
-- token; the translation IDs come from a file the caller already resolved
-- tenant-scoped.
-- Oldest first per translation, so the rendered block reads chronologically.
SELECT translation_id, content
FROM segment_comments
WHERE translation_id = ANY($1::bigint[]) AND deleted_at IS NULL
ORDER BY translation_id, created_at ASC;

-- name: CreateComment :one
INSERT INTO segment_comments (translation_id, user_id, content)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateAIComment :one
-- AI/system-authored comment (user_id NULL) — written by the QE+APE step for
-- the LLM's `comment` output field.
INSERT INTO segment_comments (translation_id, user_id, content)
VALUES ($1, NULL, $2)
RETURNING *;

-- name: DeleteAICommentsForTranslations :exec
-- Internal: NOT tenant-scoped. The translation ids must already come from a
-- tenant-resolved clear (the three callers pass ids RETURNed by their own
-- ClearTranslations* query, or one id that passed GetTranslationTenant).
-- Never call it with ids straight off a request.
-- Drop the AI-authored remarks on a set of translations (the QE+APE step
-- writes them from its `comment` field).
--
-- Called wherever a translation's text is thrown away: the remark describes a
-- specific translation, so once that text is gone the remark points at nothing.
--
-- `user_id IS NULL` is the AI marker, and the reason this is not a blanket
-- delete: a person's comment is about the SEGMENT and outlives any one
-- translation of it. Removing it would destroy their work with no undo.
DELETE FROM segment_comments
WHERE translation_id = ANY(@translation_ids::bigint[]) AND user_id IS NULL;

-- name: GetCommentOnTranslation :one
-- One live comment on one translation. The caller has already resolved the
-- translation tenant-scoped; the translation_id match keeps a comment id from
-- another translation (or tenant) out.
SELECT sc.id, sc.translation_id, sc.user_id, sc.content, sc.created_at,
       u.name AS author_name
FROM segment_comments sc
LEFT JOIN users u ON u.id = sc.user_id
WHERE sc.id = @id AND sc.translation_id = @translation_id AND sc.deleted_at IS NULL;

-- name: SoftDeleteComment :execrows
-- Soft delete. A user deletes their own comments; AI
-- comments (user_id IS NULL) are deletable by any user with access — without
-- this they'd be permanent. Scoped to the translation the caller resolved
-- tenant-scoped, so a comment id alone reaches nothing.
UPDATE segment_comments SET deleted_at = now(), updated_at = now()
WHERE id = @id AND translation_id = @translation_id AND deleted_at IS NULL
  AND (user_id = @user_id OR user_id IS NULL);

-- name: CountCommentsByFile :many
SELECT t.id AS translation_id, COUNT(sc.id)::int AS comment_count
FROM translations t
JOIN segment_comments sc ON sc.translation_id = t.id
JOIN segments s ON s.id = t.segment_id
WHERE s.file_id = $1 AND t.target_language = $2 AND sc.deleted_at IS NULL
GROUP BY t.id;
