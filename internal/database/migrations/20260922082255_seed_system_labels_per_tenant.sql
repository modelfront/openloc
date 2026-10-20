-- +goose Up
-- +goose StatementBegin

-- Every tenant gets its two built-in labels. The previous
-- migration seeded the tenants that existed at the time; this makes it true for
-- every tenant created since and every one created from now on.
--
-- A trigger rather than application code: tenants are created from five places
-- (self-serve signup, platform provisioning, OAuth first login, the seeder, the
-- test fixtures), a sixth is easy to add, and a tenant that missed the labels
-- has no Customer and no Domain with nothing to recreate them. Same reason
-- migration 031 puts the notification cleanup in a trigger.

CREATE OR REPLACE FUNCTION seed_tenant_system_labels() RETURNS trigger AS $$
BEGIN
    INSERT INTO label_definitions (tenant_id, name, system_key)
    VALUES (NEW.id, 'Customer', 'customer'),
           (NEW.id, 'Domain', 'domain')
    -- Idempotent against the partial unique index, so a restore or a replayed
    -- insert cannot fail on a tenant that already has them.
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tenants_seed_system_labels
    AFTER INSERT ON tenants
    FOR EACH ROW
    EXECUTE FUNCTION seed_tenant_system_labels();

-- Backfill: tenants created between the two migrations (a seed run, a signup)
-- have none. Matches on system_key, so a tenant that renamed its Customer
-- label is left alone rather than given a second one.
INSERT INTO label_definitions (tenant_id, name, system_key)
SELECT t.id, v.name, v.key
FROM tenants t
CROSS JOIN (VALUES ('Customer', 'customer'), ('Domain', 'domain')) AS v(name, key)
WHERE NOT EXISTS (
    SELECT 1 FROM label_definitions d
    WHERE d.tenant_id = t.id AND d.system_key = v.key AND d.deleted_at IS NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS tenants_seed_system_labels ON tenants;
DROP FUNCTION IF EXISTS seed_tenant_system_labels();

-- +goose StatementEnd
