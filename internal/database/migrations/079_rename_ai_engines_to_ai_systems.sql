-- +goose Up
-- Product rename: "AI engines" → "AI systems". Renames the tables, the
-- referencing FK columns, the explicitly-named indexes, and the purpose CHECK.
-- The internal row FK column ai_system_rows.engine_id is left as-is (it points at
-- the systems table but keeping the bare name avoids churn/collision with the
-- unrelated mt_engine_id). MT engines (mt_engines) are a different concept and
-- are untouched.
ALTER TABLE ai_engines RENAME TO ai_systems;
ALTER TABLE ai_engine_rows RENAME TO ai_system_rows;

ALTER TABLE workflow_steps RENAME COLUMN ai_engine_id TO ai_system_id;
ALTER TABLE settings_mt_assignments RENAME COLUMN ai_engine_id TO ai_system_id;

ALTER INDEX idx_ai_engines_tenant RENAME TO idx_ai_systems_tenant;
ALTER INDEX idx_ai_engine_rows_engine RENAME TO idx_ai_system_rows_engine;
ALTER INDEX idx_ai_engine_rows_system_prompt RENAME TO idx_ai_system_rows_system_prompt;
ALTER INDEX idx_settings_mt_assignments_ai_engine RENAME TO idx_settings_mt_assignments_ai_system;

ALTER TABLE ai_systems RENAME CONSTRAINT ai_engines_purpose_check TO ai_systems_purpose_check;

-- +goose Down
ALTER TABLE ai_systems RENAME CONSTRAINT ai_systems_purpose_check TO ai_engines_purpose_check;

ALTER INDEX idx_settings_mt_assignments_ai_system RENAME TO idx_settings_mt_assignments_ai_engine;
ALTER INDEX idx_ai_system_rows_system_prompt RENAME TO idx_ai_engine_rows_system_prompt;
ALTER INDEX idx_ai_system_rows_engine RENAME TO idx_ai_engine_rows_engine;
ALTER INDEX idx_ai_systems_tenant RENAME TO idx_ai_engines_tenant;

ALTER TABLE settings_mt_assignments RENAME COLUMN ai_system_id TO ai_engine_id;
ALTER TABLE workflow_steps RENAME COLUMN ai_system_id TO ai_engine_id;

ALTER TABLE ai_system_rows RENAME TO ai_engine_rows;
ALTER TABLE ai_systems RENAME TO ai_engines;
