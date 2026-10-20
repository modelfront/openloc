-- +goose Up
-- A tenant is a customer organization or the one shared, read-only demo
-- organization. demo_version records which demo dataset
-- built it, so a code change to the dataset rebuilds it.
ALTER TABLE tenants ADD COLUMN kind text NOT NULL DEFAULT 'customer'
    CHECK (kind IN ('customer', 'demo'));
ALTER TABLE tenants ADD COLUMN demo_version integer;
ALTER TABLE tenants ADD COLUMN demo_built_at timestamptz;
CREATE UNIQUE INDEX tenants_one_demo ON tenants ((kind)) WHERE kind = 'demo';

-- +goose Down
DROP INDEX IF EXISTS tenants_one_demo;
ALTER TABLE tenants DROP COLUMN IF EXISTS demo_built_at;
ALTER TABLE tenants DROP COLUMN IF EXISTS demo_version;
ALTER TABLE tenants DROP COLUMN IF EXISTS kind;
