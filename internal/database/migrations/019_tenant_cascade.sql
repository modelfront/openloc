-- +goose Up
-- +goose StatementBegin

-- Add ON DELETE CASCADE to every FK that references tenants(id) but was
-- created without it in migration 001 + 005. Without these, deleting a
-- tenant via DELETE /api/tenants/current 500s with FK violation — the
-- "destroy account" UX every TMS supports breaks. Tables already covered
-- in 001 (analysis_weights, cost_rates, notifications, org_notification_rules,
-- qa_profiles, term_bases) are skipped here.

ALTER TABLE invites          DROP CONSTRAINT invites_tenant_id_fkey,
                             ADD  CONSTRAINT invites_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE mt_engines       DROP CONSTRAINT mt_engines_tenant_id_fkey,
                             ADD  CONSTRAINT mt_engines_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE projects         DROP CONSTRAINT projects_tenant_id_fkey,
                             ADD  CONSTRAINT projects_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE templates        DROP CONSTRAINT templates_tenant_id_fkey,
                             ADD  CONSTRAINT templates_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE tm_collections   DROP CONSTRAINT tm_collections_tenant_id_fkey,
                             ADD  CONSTRAINT tm_collections_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE tm_imports       DROP CONSTRAINT tm_imports_tenant_id_fkey,
                             ADD  CONSTRAINT tm_imports_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

ALTER TABLE users            DROP CONSTRAINT users_tenant_id_fkey,
                             ADD  CONSTRAINT users_tenant_id_fkey
                             FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

-- settings_profiles was added in migration 005 with the inline syntax (no
-- named constraint). Postgres auto-named it `settings_profiles_tenant_id_fkey`.
ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tenant_id_fkey,
                              ADD  CONSTRAINT settings_profiles_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore the no-CASCADE constraints (matches pre-019 behavior).
ALTER TABLE invites           DROP CONSTRAINT invites_tenant_id_fkey,
                              ADD  CONSTRAINT invites_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE mt_engines        DROP CONSTRAINT mt_engines_tenant_id_fkey,
                              ADD  CONSTRAINT mt_engines_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE projects          DROP CONSTRAINT projects_tenant_id_fkey,
                              ADD  CONSTRAINT projects_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE templates         DROP CONSTRAINT templates_tenant_id_fkey,
                              ADD  CONSTRAINT templates_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE tm_collections    DROP CONSTRAINT tm_collections_tenant_id_fkey,
                              ADD  CONSTRAINT tm_collections_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE tm_imports        DROP CONSTRAINT tm_imports_tenant_id_fkey,
                              ADD  CONSTRAINT tm_imports_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE users             DROP CONSTRAINT users_tenant_id_fkey,
                              ADD  CONSTRAINT users_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tenant_id_fkey,
                              ADD  CONSTRAINT settings_profiles_tenant_id_fkey
                              FOREIGN KEY (tenant_id) REFERENCES tenants(id);

-- +goose StatementEnd
