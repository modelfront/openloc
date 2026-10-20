-- +goose Up
-- An engine row may use ModelFront's native QP+APE instead of
-- an LLM system prompt. Such a row carries no system prompt (nor segment prompt
-- / bad-output action), so system_prompt_id becomes nullable — NULL marks a
-- ModelFront-native row.
ALTER TABLE ai_engine_rows ALTER COLUMN system_prompt_id DROP NOT NULL;

-- +goose Down
-- Reinstate the NOT NULL (fails if any ModelFront rows exist — clear those first).
ALTER TABLE ai_engine_rows ALTER COLUMN system_prompt_id SET NOT NULL;
