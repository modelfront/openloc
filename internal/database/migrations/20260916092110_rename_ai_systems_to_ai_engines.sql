-- +goose Up
-- Product rename: "AI systems" → "AI engines". Renames the two tables, the
-- referencing FK columns, the explicitly-named indexes and the constraints
-- that carry the ai_system name. Primary keys, sequences and the older FK
-- constraints already carry the ai_engine name and are untouched.
-- MT engines (mt_engines) are a different concept and are untouched.
-- ai_engine_rows.engine_id keeps its bare name (it points at ai_engines).
ALTER TABLE ai_systems RENAME TO ai_engines;
ALTER TABLE ai_system_rows RENAME TO ai_engine_rows;

ALTER TABLE workflow_steps RENAME COLUMN ai_system_id TO ai_engine_id;
ALTER TABLE settings_mt_assignments RENAME COLUMN ai_system_id TO ai_engine_id;
ALTER TABLE step_definitions RENAME COLUMN ai_system_id TO ai_engine_id;

ALTER INDEX idx_ai_systems_tenant RENAME TO idx_ai_engines_tenant;
ALTER INDEX idx_ai_system_rows_engine RENAME TO idx_ai_engine_rows_engine;
ALTER INDEX idx_ai_system_rows_system_prompt RENAME TO idx_ai_engine_rows_system_prompt;
ALTER INDEX idx_ai_system_rows_token RENAME TO idx_ai_engine_rows_token;
ALTER INDEX idx_settings_mt_assignments_ai_system RENAME TO idx_settings_mt_assignments_ai_engine;

ALTER TABLE ai_engines RENAME CONSTRAINT ai_systems_purpose_check TO ai_engines_purpose_check;
ALTER TABLE step_definitions RENAME CONSTRAINT step_definitions_ai_system_id_fkey TO step_definitions_ai_engine_id_fkey;

-- +goose Down
ALTER TABLE step_definitions RENAME CONSTRAINT step_definitions_ai_engine_id_fkey TO step_definitions_ai_system_id_fkey;
ALTER TABLE ai_engines RENAME CONSTRAINT ai_engines_purpose_check TO ai_systems_purpose_check;

ALTER INDEX idx_settings_mt_assignments_ai_engine RENAME TO idx_settings_mt_assignments_ai_system;
ALTER INDEX idx_ai_engine_rows_token RENAME TO idx_ai_system_rows_token;
ALTER INDEX idx_ai_engine_rows_system_prompt RENAME TO idx_ai_system_rows_system_prompt;
ALTER INDEX idx_ai_engine_rows_engine RENAME TO idx_ai_system_rows_engine;
ALTER INDEX idx_ai_engines_tenant RENAME TO idx_ai_systems_tenant;

ALTER TABLE step_definitions RENAME COLUMN ai_engine_id TO ai_system_id;
ALTER TABLE settings_mt_assignments RENAME COLUMN ai_engine_id TO ai_system_id;
ALTER TABLE workflow_steps RENAME COLUMN ai_engine_id TO ai_system_id;

ALTER TABLE ai_engine_rows RENAME TO ai_system_rows;
ALTER TABLE ai_engines RENAME TO ai_systems;
