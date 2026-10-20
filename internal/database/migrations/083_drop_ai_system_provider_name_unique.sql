-- +goose Up
-- Allow duplicate names for AI systems and provider configs. The tenant-wide
-- UNIQUE(tenant_id, name) made the "New AI system" / "New provider" default
-- names collide on the second create, and the collision surfaced as a confusing
-- failure. Names are convenience labels here (each row has an id + a
-- provider/model chip that distinguishes look-alikes), so uniqueness isn't
-- worth the friction. Unlike ai_prompts / segment_prompts, neither table
-- upserts by name, so nothing depends on the constraint.
-- ai_systems' constraint kept its original name through the ai_engines rename
-- (migration 079 didn't rename it).
ALTER TABLE ai_systems DROP CONSTRAINT ai_engines_tenant_id_name_key;
ALTER TABLE ai_providers DROP CONSTRAINT ai_providers_tenant_id_name_key;

-- +goose Down
ALTER TABLE ai_providers ADD CONSTRAINT ai_providers_tenant_id_name_key UNIQUE (tenant_id, name);
ALTER TABLE ai_systems ADD CONSTRAINT ai_engines_tenant_id_name_key UNIQUE (tenant_id, name);
