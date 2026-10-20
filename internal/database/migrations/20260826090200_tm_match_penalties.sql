-- +goose Up
-- +goose StatementBegin

-- Configurable TM match penalties. All three were hardcoded constants (or, for
-- the per-TM one, designed and never built). Percentages rather than
-- 0–1 floats: that's the spec and what the UI shows.
--
-- The defaults reproduce today's behaviour exactly, so this migration is a
-- no-op for existing projects:
--   sublanguage 1% = the existing sublanguage-penalty const
--   near_exact  1% = the 0.99 NearExactScore band
--   per-TM      0% = no penalty, matching the designed default
--
-- CHECK constraints because the UI's 0–100 bounds have to hold in the backend
-- too — the API accepts these fields directly.
ALTER TABLE settings_profiles
  ADD COLUMN sublanguage_penalty_pct int NOT NULL DEFAULT 1
    CHECK (sublanguage_penalty_pct BETWEEN 0 AND 100),
  ADD COLUMN near_exact_penalty_pct int NOT NULL DEFAULT 1
    CHECK (near_exact_penalty_pct BETWEEN 0 AND 100);

-- Per-TM penalty, docking every match from a lower-trust TM
-- (client-provided, legacy, shared).
ALTER TABLE settings_tm_assignments
  ADD COLUMN penalty_pct int NOT NULL DEFAULT 0
    CHECK (penalty_pct BETWEEN 0 AND 100);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings_tm_assignments DROP COLUMN IF EXISTS penalty_pct;
ALTER TABLE settings_profiles
  DROP COLUMN IF EXISTS near_exact_penalty_pct,
  DROP COLUMN IF EXISTS sublanguage_penalty_pct;
-- +goose StatementEnd
