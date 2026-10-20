-- +goose Up
-- Pin a specific provider model on a prompt. The
-- AI-as-MT runtime calls this exact model instead of the hardcoded default.
-- Nullable: ModelFront prompts (QP+APE) have no chat-model variant, so they
-- carry no model. For OpenAI/Claude the handler requires a valid catalog model.
ALTER TABLE ai_prompts ADD COLUMN model text;

-- +goose Down
ALTER TABLE ai_prompts DROP COLUMN model;
