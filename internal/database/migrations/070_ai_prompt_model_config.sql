-- +goose Up
-- Per-prompt model parameters: temperature, top_p, etc.,
-- keyed by the provider's parameter catalog (internal/ai/params.go). Stored as a
-- JSON object; validated on save. Not yet threaded into the runtime provider
-- call (deferred follow-up).
-- Nullable so existing CreateAIPrompt callers (seed, tests) that don't set it
-- store NULL; the HTTP handler always writes at least '{}', and the response
-- maps NULL → {} for the client.
ALTER TABLE ai_prompts ADD COLUMN model_config jsonb;

-- +goose Down
ALTER TABLE ai_prompts DROP COLUMN model_config;
