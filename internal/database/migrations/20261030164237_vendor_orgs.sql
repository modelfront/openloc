-- +goose Up
-- A vendor organization belongs to exactly one client organization: its
-- client_tenant_id. Only vendor orgs have one.
ALTER TABLE tenants DROP CONSTRAINT tenants_kind_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_kind_check
    CHECK (kind IN ('customer', 'demo', 'vendor'));
ALTER TABLE tenants ADD COLUMN client_tenant_id bigint
    REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE tenants ADD CONSTRAINT tenants_vendor_has_client
    CHECK ((kind = 'vendor') = (client_tenant_id IS NOT NULL));
CREATE INDEX tenants_client_tenant_id_idx ON tenants (client_tenant_id)
    WHERE client_tenant_id IS NOT NULL;

-- A 'vendor_org' invite lives in the client tenant and, once accepted,
-- creates the vendor org named vendor_org_name with the invitee as its admin.
ALTER TABLE invites ADD COLUMN kind text NOT NULL DEFAULT 'member'
    CONSTRAINT invites_kind_check CHECK (kind IN ('member', 'vendor_org'));
ALTER TABLE invites ADD COLUMN vendor_org_name text;
ALTER TABLE invites ADD CONSTRAINT invites_vendor_org_has_name
    CHECK ((kind = 'vendor_org') = (vendor_org_name IS NOT NULL));

-- +goose Down
-- Without the kind column a vendor-org invite would read as an admin invite
-- into the client org, so they go; vendor orgs stay as stand-alone orgs.
DELETE FROM invites WHERE kind = 'vendor_org';
ALTER TABLE invites DROP CONSTRAINT IF EXISTS invites_vendor_org_has_name;
ALTER TABLE invites DROP COLUMN IF EXISTS vendor_org_name;
ALTER TABLE invites DROP COLUMN IF EXISTS kind;

ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_vendor_has_client;
UPDATE tenants SET kind = 'customer' WHERE kind = 'vendor';
DROP INDEX IF EXISTS tenants_client_tenant_id_idx;
ALTER TABLE tenants DROP COLUMN IF EXISTS client_tenant_id;
ALTER TABLE tenants DROP CONSTRAINT tenants_kind_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_kind_check
    CHECK (kind IN ('customer', 'demo'));
