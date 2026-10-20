-- +goose Up
-- The built-in workflow is named "Pre-translation": the Workflow card's Raw MT
-- mode applies it. Library names are unique among a tenant's own rows only
-- (system rows sit outside that index), so a tenant workflow already named
-- "Pre-translation" does not collide.
--
-- Deploy order: roll the new binary first, then migrate. Both binaries find
-- the row by system_key, so either name works with either; an organization
-- provisioned by the OLD binary after this ran would keep the old name, since
-- provisioning inserts with ON CONFLICT DO NOTHING.
UPDATE workflows
   SET name = 'Pre-translation', updated_at = now()
 WHERE system_key = 'pretranslate_only' AND deleted_at IS NULL;

-- +goose Down
UPDATE workflows
   SET name = 'Pre-translation only', updated_at = now()
 WHERE system_key = 'pretranslate_only' AND deleted_at IS NULL;
