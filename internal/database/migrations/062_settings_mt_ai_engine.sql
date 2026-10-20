-- +goose Up
-- AI-as-MT: an MT assignment can now point at EITHER a classic MT
-- engine (mt_engine_id) OR an AI engine of type 'mt' (ai_engine_id), never both.
-- The wildcard row (source_language = '*' AND target_language = '*') is the
-- project-wide default; specific (source, target) rows are per-language overrides.
ALTER TABLE settings_mt_assignments
    ALTER COLUMN mt_engine_id DROP NOT NULL,
    ADD COLUMN ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE CASCADE;

ALTER TABLE settings_mt_assignments
    ADD CONSTRAINT settings_mt_assignment_engine_xor
    CHECK ((mt_engine_id IS NOT NULL) <> (ai_engine_id IS NOT NULL));

CREATE INDEX idx_settings_mt_assignments_ai_engine ON settings_mt_assignments (ai_engine_id);

-- +goose Down
DROP INDEX IF EXISTS idx_settings_mt_assignments_ai_engine;
ALTER TABLE settings_mt_assignments DROP CONSTRAINT IF EXISTS settings_mt_assignment_engine_xor;
-- AI-only rows vanish with the column; any remaining rows have a non-NULL
-- mt_engine_id, so restoring NOT NULL is safe.
ALTER TABLE settings_mt_assignments DROP COLUMN ai_engine_id;
ALTER TABLE settings_mt_assignments ALTER COLUMN mt_engine_id SET NOT NULL;
