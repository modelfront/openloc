-- ============================================================
-- Spell check dictionary (per project + language)
-- ============================================================

-- name: ListSpellcheckDictionary :many
SELECT * FROM spellcheck_dictionary
WHERE project_id = $1 AND tenant_id = $2 AND language = $3 AND deleted_at IS NULL
ORDER BY word;

-- name: ListSpellcheckDictionaryWords :many
-- Just the words — what the check path needs to subtract matches. Kept
-- separate from the full-row list so the hot path doesn't haul timestamps and
-- user ids it never reads.
SELECT word FROM spellcheck_dictionary
WHERE project_id = $1 AND tenant_id = $2 AND language = $3 AND deleted_at IS NULL;

-- name: AddSpellcheckDictionaryWord :one
-- Idempotent: "Ignore all" on a word two linguists both hit shouldn't 409.
-- Adding a removed word brings its row back.
INSERT INTO spellcheck_dictionary (tenant_id, project_id, language, word, created_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id, language, word) DO UPDATE
    SET deleted_at = NULL
RETURNING *;

-- name: SoftDeleteSpellcheckDictionaryWord :execrows
-- Matches the project in the URL as well as the tenant: a word is removed only
-- through the project it belongs to. Zero rows when it is already removed.
UPDATE spellcheck_dictionary SET deleted_at = now()
WHERE id = $1 AND tenant_id = $2 AND project_id = $3 AND deleted_at IS NULL;
