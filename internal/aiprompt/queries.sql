-- name: ListAIPrompts :many
-- Defaults first (stable stock templates), then customs by age.
SELECT * FROM ai_prompts WHERE tenant_id = $1 ORDER BY is_default DESC, created_at ASC;

-- name: UpsertDefaultAIPrompt :exec
-- Provision one built-in template — and keep it current. Default
-- prompts are read-only, so a code-shipped wording update may safely refresh
-- the row (the conditional DO UPDATE); a user's pre-existing CUSTOM prompt
-- with the same name is never touched (is_default = false fails the WHERE).
INSERT INTO ai_prompts (
    tenant_id, name, prompt_text,
    out_status, out_text, out_note, out_comments, segment_context, is_default
)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(segment_context)::text[], '{}'), true)
ON CONFLICT (tenant_id, name) DO UPDATE SET
    prompt_text     = EXCLUDED.prompt_text,
    out_status      = EXCLUDED.out_status,
    out_text        = EXCLUDED.out_text,
    out_note        = EXCLUDED.out_note,
    out_comments    = EXCLUDED.out_comments,
    segment_context = EXCLUDED.segment_context,
    updated_at      = now()
WHERE ai_prompts.is_default
  AND (ai_prompts.prompt_text     IS DISTINCT FROM EXCLUDED.prompt_text
    OR ai_prompts.out_status      IS DISTINCT FROM EXCLUDED.out_status
    OR ai_prompts.out_text        IS DISTINCT FROM EXCLUDED.out_text
    OR ai_prompts.out_note        IS DISTINCT FROM EXCLUDED.out_note
    OR ai_prompts.out_comments    IS DISTINCT FROM EXCLUDED.out_comments
    OR ai_prompts.segment_context IS DISTINCT FROM EXCLUDED.segment_context);

-- name: GetAIPrompt :one
SELECT * FROM ai_prompts WHERE id = $1 AND tenant_id = $2;

-- name: CreateAIPrompt :one
INSERT INTO ai_prompts (
    tenant_id, name, prompt_text,
    out_status, out_text, out_note, out_comments, segment_context
)
-- segment_context is text[] NOT NULL; COALESCE so a caller that leaves it nil
-- gets the empty set rather than a not-null violation (same shape as the
-- settings jsonb on ai_engine_rows).
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(segment_context)::text[], '{}'))
RETURNING *;

-- name: UpdateAIPrompt :one
UPDATE ai_prompts
SET name = $3, prompt_text = $4,
    out_status = $5, out_text = $6, out_note = $7, out_comments = $8,
    segment_context = COALESCE(sqlc.narg(segment_context)::text[], '{}'),
    updated_at = now()
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: DeleteAIPrompt :exec
DELETE FROM ai_prompts WHERE id = $1 AND tenant_id = $2;

-- name: ListAIPromptLanguageAdditions :many
-- Every addition belonging to a tenant's prompts, prompt by prompt. One query
-- for the whole list rather than one per prompt: the AI page always renders all
-- of them, and a prompt with no additions must still cost nothing.
SELECT a.*
FROM ai_prompt_language_additions a
JOIN ai_prompts p ON p.id = a.prompt_id
WHERE p.tenant_id = $1
ORDER BY a.prompt_id, a.position, a.id;

-- name: ListAIPromptLanguageAdditionsForPrompt :many
-- Internal: keyed on prompt_id alone. Every caller resolves the prompt
-- through the tenant-scoped GetAIPrompt/UpdateAIPrompt first, or reads the
-- prompt CreateAIPrompt just inserted for its tenant.
-- One prompt's additions, in append order. Used on the execution path, where
-- only the engine row's own prompt matters.
SELECT * FROM ai_prompt_language_additions
WHERE prompt_id = $1
ORDER BY position, id;

-- name: CreateAIPromptLanguageAddition :exec
-- Internal: keyed on prompt_id alone. Every caller resolves the prompt
-- through the tenant-scoped GetAIPrompt/UpdateAIPrompt first.
INSERT INTO ai_prompt_language_additions (prompt_id, target_languages, addition_text, position)
VALUES ($1, $2, $3, $4);

-- name: DeleteAIPromptLanguageAdditions :exec
-- Internal: keyed on prompt_id alone. Every caller resolves the prompt
-- through the tenant-scoped GetAIPrompt/UpdateAIPrompt first.
-- Replace-all: the write paths clear a prompt's additions and re-insert the
-- submitted set inside one transaction. Positions are assigned from the
-- submitted order, so preserving row ids across an edit would buy nothing.
DELETE FROM ai_prompt_language_additions WHERE prompt_id = $1;

-- name: CountAIEnginesForPrompt :one
-- Internal: keyed on prompt_id alone. Every caller resolves the prompt
-- through the tenant-scoped GetAIPrompt/UpdateAIPrompt first.
-- How many AI engine rows reference this (system) prompt. Used by HandleDelete
-- to 409 instead of relying on the ON DELETE RESTRICT FK (which would surface
-- as an opaque 500).
SELECT count(*)::int AS count
FROM ai_engine_rows
WHERE system_prompt_id = $1;
