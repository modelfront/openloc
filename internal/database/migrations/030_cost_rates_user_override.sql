-- +goose Up
-- Per-user (vendor) rate overrides on cost_rates (Pass 8 slice 8.2,
-- audit gap 2.2). Adds nullable user_id FK so an admin can capture a
-- specific translator's negotiated rate that overrides the org-default
-- rate for the same (source, target) pair.
--
-- Lookup precedence (resolved in Go, not SQL): user-specific → org default.
-- Used by the productivity report to estimate cost per translator.
-- Project-level Analysis tab cost stays org-default (computed pre-assignment).
--
-- The original UNIQUE constraint allowed only one row per
-- (tenant, source, target). With per-user rates we need:
--   - at most one ORG-LEVEL rate per (tenant, source, target)        — partial idx WHERE user_id IS NULL
--   - at most one PER-USER rate per (tenant, user, source, target)  — partial idx WHERE user_id IS NOT NULL
--
-- ON DELETE CASCADE matches the existing user_language_pairs convention:
-- when a user is hard-deleted, their rate row goes with them. Soft-delete
-- (users.deactivated_at) preserves the user record AND the rate, so
-- procurement keeps historical pay-rate info.
ALTER TABLE cost_rates
    ADD COLUMN user_id bigint REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE cost_rates
    DROP CONSTRAINT cost_rates_tenant_id_source_language_target_language_key;

CREATE UNIQUE INDEX cost_rates_org_unique
    ON cost_rates (tenant_id, source_language, target_language)
    WHERE user_id IS NULL;

CREATE UNIQUE INDEX cost_rates_user_unique
    ON cost_rates (tenant_id, user_id, source_language, target_language)
    WHERE user_id IS NOT NULL;

CREATE INDEX cost_rates_user_id_idx ON cost_rates (user_id) WHERE user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS cost_rates_user_id_idx;
DROP INDEX IF EXISTS cost_rates_user_unique;
DROP INDEX IF EXISTS cost_rates_org_unique;

ALTER TABLE cost_rates
    ADD CONSTRAINT cost_rates_tenant_id_source_language_target_language_key
    UNIQUE (tenant_id, source_language, target_language);

ALTER TABLE cost_rates DROP COLUMN user_id;
