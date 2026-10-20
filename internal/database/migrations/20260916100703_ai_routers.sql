-- +goose Up
-- +goose StatementBegin

-- AI routers: what a hybrid / ai workflow step points at instead of an AI
-- engine. A router maps target languages to QE+APE engines and may name a
-- default engine for every other language. Tenant-scoped, soft-deleted.
CREATE TABLE ai_routers (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name              text NOT NULL,
    default_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE UNIQUE INDEX idx_ai_routers_tenant_name
    ON ai_routers (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE INDEX idx_ai_routers_tenant
    ON ai_routers (tenant_id) WHERE deleted_at IS NULL;

CREATE INDEX idx_ai_routers_default_engine
    ON ai_routers (default_engine_id);

-- One rule = one target language → one engine. The engine FK cascades for
-- tenant teardown only; the engine delete handler refuses (409) while any
-- live router still references the engine.
CREATE TABLE ai_router_rules (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    router_id   bigint NOT NULL REFERENCES ai_routers(id) ON DELETE CASCADE,
    target_lang text NOT NULL,
    engine_id   bigint NOT NULL REFERENCES ai_engines(id) ON DELETE CASCADE,
    sort_order  integer NOT NULL DEFAULT 0,
    UNIQUE (router_id, target_lang)
);

CREATE INDEX idx_ai_router_rules_engine
    ON ai_router_rules (engine_id);

ALTER TABLE step_definitions
    ADD COLUMN ai_router_id bigint REFERENCES ai_routers(id) ON DELETE SET NULL;
ALTER TABLE workflow_steps
    ADD COLUMN ai_router_id bigint REFERENCES ai_routers(id) ON DELETE SET NULL;

-- Every engine a step (library definition or profile snapshot) points at gets
-- one router, named after the engine, with that engine as the default — so
-- every existing step keeps running exactly as before. A tenant with two
-- same-named engines gets "Name" and "Name (2)".
WITH referenced AS (
    SELECT e.id AS engine_id, e.tenant_id, e.name
    FROM ai_engines e
    WHERE e.id IN (
        SELECT ai_engine_id FROM step_definitions WHERE ai_engine_id IS NOT NULL
        UNION
        SELECT ai_engine_id FROM workflow_steps WHERE ai_engine_id IS NOT NULL
    )
), named AS (
    SELECT engine_id, tenant_id, name,
           row_number() OVER (PARTITION BY tenant_id, lower(name) ORDER BY engine_id) AS rn
    FROM referenced
)
INSERT INTO ai_routers (tenant_id, name, default_engine_id)
SELECT tenant_id,
       CASE WHEN rn = 1 THEN name ELSE name || ' (' || rn || ')' END,
       engine_id
FROM named;

UPDATE step_definitions sd
SET ai_router_id = r.id
FROM ai_routers r
WHERE sd.ai_engine_id IS NOT NULL
  AND r.default_engine_id = sd.ai_engine_id
  AND r.tenant_id = sd.tenant_id;

-- A profile step has no tenant of its own; it borrows the owning profile's,
-- so the router match is scoped the same way the step_definitions one is.
UPDATE workflow_steps ws
SET ai_router_id = r.id
FROM ai_routers r, settings_profiles sp
WHERE ws.ai_engine_id IS NOT NULL
  AND sp.id = ws.settings_profile_id
  AND r.default_engine_id = ws.ai_engine_id
  AND r.tenant_id = sp.tenant_id;

ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_human_no_engine;
ALTER TABLE step_definitions DROP COLUMN ai_engine_id;
-- hybrid/ai steps need a router; human steps must not carry one. The router
-- FK is SET NULL on router delete, so a deleted router leaves a hybrid/ai row
-- with a NULL router, which the UI flags as stale.
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_human_no_router CHECK (step_type <> 'human' OR ai_router_id IS NULL);

ALTER TABLE workflow_steps DROP COLUMN ai_engine_id;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE workflow_steps
    ADD COLUMN ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL;
ALTER TABLE step_definitions
    ADD COLUMN ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL;

UPDATE workflow_steps ws
SET ai_engine_id = r.default_engine_id
FROM ai_routers r
WHERE r.id = ws.ai_router_id;

UPDATE step_definitions sd
SET ai_engine_id = r.default_engine_id
FROM ai_routers r
WHERE r.id = sd.ai_router_id;

ALTER TABLE step_definitions DROP CONSTRAINT step_definitions_human_no_router;
ALTER TABLE step_definitions
    ADD CONSTRAINT step_definitions_human_no_engine CHECK (step_type <> 'human' OR ai_engine_id IS NULL);

ALTER TABLE step_definitions DROP COLUMN ai_router_id;
ALTER TABLE workflow_steps DROP COLUMN ai_router_id;

DROP TABLE ai_router_rules;
DROP TABLE ai_routers;

-- +goose StatementEnd
