-- +goose NO TRANSACTION
-- +goose Up
-- translation_history.tenant_id: the tenant of the row's translation, so the
-- dashboards can read one tenant's history in a time range through an index
-- instead of walking every translation of the tenant. A BEFORE INSERT trigger
-- fills it from translation → segment → file → project, so no writer passes
-- it and none can get it wrong; a project never changes tenant.
--
-- Each statement commits on its own. Adding a column with no default only
-- touches the catalog. The backfill updates 10,000 rows per transaction in id
-- order, so it holds no long lock and can run while the app writes; rows
-- inserted meanwhile get their tenant from the trigger. The NOT NULL check is
-- added NOT VALID and then validated, which scans the table without blocking
-- reads or writes. Every statement is safe to re-run after a partial failure.
ALTER TABLE translation_history ADD COLUMN IF NOT EXISTS tenant_id bigint;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION translation_history_set_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id IS NULL THEN
        SELECT p.tenant_id INTO NEW.tenant_id
        FROM translations t
        JOIN segments s ON s.id = t.segment_id
        JOIN files f ON f.id = s.file_id
        JOIN projects p ON p.id = f.project_id
        WHERE t.id = NEW.translation_id;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS translation_history_set_tenant ON translation_history;
CREATE TRIGGER translation_history_set_tenant
    BEFORE INSERT ON translation_history
    FOR EACH ROW EXECUTE FUNCTION translation_history_set_tenant();

-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE backfill_translation_history_tenant()
LANGUAGE plpgsql AS $$
DECLARE
    last_id bigint := 0;
    max_id  bigint;
BEGIN
    SELECT coalesce(max(id), 0) INTO max_id FROM translation_history;
    WHILE last_id < max_id LOOP
        UPDATE translation_history th
        SET tenant_id = p.tenant_id
        FROM translations t
        JOIN segments s ON s.id = t.segment_id
        JOIN files f ON f.id = s.file_id
        JOIN projects p ON p.id = f.project_id
        WHERE th.id > last_id AND th.id <= last_id + 10000
          AND th.tenant_id IS NULL
          AND t.id = th.translation_id;
        last_id := last_id + 10000;
        COMMIT;
    END LOOP;
END $$;
-- +goose StatementEnd

CALL backfill_translation_history_tenant();
DROP PROCEDURE backfill_translation_history_tenant();

ALTER TABLE translation_history DROP CONSTRAINT IF EXISTS translation_history_tenant_id_not_null;
ALTER TABLE translation_history
    ADD CONSTRAINT translation_history_tenant_id_not_null CHECK (tenant_id IS NOT NULL) NOT VALID;
ALTER TABLE translation_history VALIDATE CONSTRAINT translation_history_tenant_id_not_null;

-- +goose Down
DROP TRIGGER IF EXISTS translation_history_set_tenant ON translation_history;
DROP FUNCTION IF EXISTS translation_history_set_tenant();
ALTER TABLE translation_history DROP COLUMN IF EXISTS tenant_id;
