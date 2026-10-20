-- +goose Up
-- The built-in prompt templates are matched by (tenant_id, name) in
-- UpsertDefaultAIPrompt, so renaming one in code inserts a second row instead of
-- renaming the existing one — every tenant would list two default QE+APE
-- prompts, the stale one still selectable. Rename in place first.
--
-- Scoped to is_default: a user's own prompt that happens to be called
-- "Default QE/APE" is theirs, and the upsert already refuses to touch it.
-- Guarded against the collision where a tenant somehow holds both names.
-- +goose StatementBegin
UPDATE ai_prompts p
SET name = 'Default QE+APE', updated_at = now()
WHERE p.name = 'Default QE/APE'
  AND p.is_default
  AND NOT EXISTS (
    SELECT 1 FROM ai_prompts other
    WHERE other.tenant_id = p.tenant_id AND other.name = 'Default QE+APE'
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE ai_prompts p
SET name = 'Default QE/APE', updated_at = now()
WHERE p.name = 'Default QE+APE'
  AND p.is_default
  AND NOT EXISTS (
    SELECT 1 FROM ai_prompts other
    WHERE other.tenant_id = p.tenant_id AND other.name = 'Default QE/APE'
  );
-- +goose StatementEnd
