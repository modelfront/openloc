-- +goose Up
-- +goose StatementBegin

-- MT routers: what a project's settings profile points at for machine
-- translation. A router maps target languages to MT engines — a classic
-- engine (mt_engines) or an LLM translation engine (ai_engines, purpose 'mt')
-- — and may name a default engine for every other language. Replaces the
-- per-profile settings_mt_assignments rows. Tenant-scoped, soft-deleted.
CREATE TABLE mt_routers (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                 text NOT NULL,
    default_mt_engine_id bigint REFERENCES mt_engines(id) ON DELETE SET NULL,
    default_ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    CONSTRAINT mt_routers_default_one_engine
        CHECK (NOT (default_mt_engine_id IS NOT NULL AND default_ai_engine_id IS NOT NULL))
);

CREATE UNIQUE INDEX idx_mt_routers_tenant_name
    ON mt_routers (tenant_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX idx_mt_routers_tenant ON mt_routers (tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_mt_routers_default_mt_engine ON mt_routers (default_mt_engine_id);
CREATE INDEX idx_mt_routers_default_ai_engine ON mt_routers (default_ai_engine_id);

-- One rule = one target language → one engine (exactly one of the two ids).
-- The engine FKs cascade for tenant teardown only; the engine delete
-- handlers refuse (409) while any live router still references the engine.
CREATE TABLE mt_router_rules (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    router_id    bigint NOT NULL REFERENCES mt_routers(id) ON DELETE CASCADE,
    target_lang  text NOT NULL,
    mt_engine_id bigint REFERENCES mt_engines(id) ON DELETE CASCADE,
    ai_engine_id bigint REFERENCES ai_engines(id) ON DELETE CASCADE,
    sort_order   integer NOT NULL DEFAULT 0,
    UNIQUE (router_id, target_lang),
    CONSTRAINT mt_router_rules_one_engine
        CHECK ((mt_engine_id IS NOT NULL) <> (ai_engine_id IS NOT NULL))
);
CREATE INDEX idx_mt_router_rules_mt_engine ON mt_router_rules (mt_engine_id);
CREATE INDEX idx_mt_router_rules_ai_engine ON mt_router_rules (ai_engine_id);

ALTER TABLE settings_profiles
    ADD COLUMN mt_router_id bigint REFERENCES mt_routers(id) ON DELETE SET NULL;
CREATE INDEX idx_settings_profiles_mt_router ON settings_profiles (mt_router_id);

-- Every profile that had MT assignments gets its own router so nothing
-- changes at run time: the (*, *) wildcard row becomes the default, every
-- other row one rule for its target language (the first row per target wins;
-- routers ignore the source language). Named after the project or template
-- the profile belongs to; same-named ones get " (2)", " (3)"…
WITH owned AS (
    SELECT sp.id AS profile_id, sp.tenant_id,
           COALESCE(p.name, t.name, 'Profile ' || sp.id) || ' MT' AS base_name
    FROM settings_profiles sp
    LEFT JOIN projects p  ON p.settings_profile_id = sp.id
    LEFT JOIN templates t ON t.settings_profile_id = sp.id
    WHERE EXISTS (SELECT 1 FROM settings_mt_assignments sma WHERE sma.settings_profile_id = sp.id)
), named AS (
    SELECT profile_id, tenant_id, base_name,
           row_number() OVER (PARTITION BY tenant_id, lower(base_name) ORDER BY profile_id) AS rn
    FROM owned
), defaults AS (
    SELECT DISTINCT ON (settings_profile_id) settings_profile_id, mt_engine_id, ai_engine_id
    FROM settings_mt_assignments
    WHERE source_language = '*' AND target_language = '*'
    ORDER BY settings_profile_id, id
), created AS (
    INSERT INTO mt_routers (tenant_id, name, default_mt_engine_id, default_ai_engine_id)
    SELECT n.tenant_id,
           CASE WHEN n.rn = 1 THEN n.base_name ELSE n.base_name || ' (' || n.rn || ')' END,
           d.mt_engine_id, d.ai_engine_id
    FROM named n
    LEFT JOIN defaults d ON d.settings_profile_id = n.profile_id
    RETURNING id, name, tenant_id
), linked AS (
    UPDATE settings_profiles sp
    SET mt_router_id = c.id
    FROM named n
    JOIN created c ON c.tenant_id = n.tenant_id
                  AND c.name = CASE WHEN n.rn = 1 THEN n.base_name ELSE n.base_name || ' (' || n.rn || ')' END
    WHERE sp.id = n.profile_id
    RETURNING sp.id AS profile_id, sp.mt_router_id
)
INSERT INTO mt_router_rules (router_id, target_lang, mt_engine_id, ai_engine_id, sort_order)
SELECT l.mt_router_id, r.target_language, r.mt_engine_id, r.ai_engine_id,
       row_number() OVER (PARTITION BY l.mt_router_id ORDER BY r.id) - 1
FROM linked l
JOIN (
    SELECT DISTINCT ON (settings_profile_id, target_language) settings_profile_id, target_language, mt_engine_id, ai_engine_id, id
    FROM settings_mt_assignments
    WHERE NOT (source_language = '*' AND target_language = '*') AND target_language <> '*'
    ORDER BY settings_profile_id, target_language, id
) r ON r.settings_profile_id = l.profile_id;

DROP TABLE settings_mt_assignments;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE settings_mt_assignments (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settings_profile_id bigint NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
    mt_engine_id        bigint REFERENCES mt_engines(id) ON DELETE CASCADE,
    ai_engine_id        bigint REFERENCES ai_engines(id) ON DELETE CASCADE,
    source_language     text NOT NULL,
    target_language     text NOT NULL,
    UNIQUE (settings_profile_id, source_language, target_language),
    CHECK ((mt_engine_id IS NOT NULL) <> (ai_engine_id IS NOT NULL))
);
CREATE INDEX idx_settings_mt_lookup ON settings_mt_assignments (settings_profile_id, source_language, target_language);
CREATE INDEX idx_settings_mt_assignments_ai_engine ON settings_mt_assignments (ai_engine_id);

-- Default → the (*, *) wildcard row; rules → one row per target with the
-- owning project's source language (templates have none: '*').
INSERT INTO settings_mt_assignments (settings_profile_id, mt_engine_id, ai_engine_id, source_language, target_language)
SELECT sp.id, r.default_mt_engine_id, r.default_ai_engine_id, '*', '*'
FROM settings_profiles sp
JOIN mt_routers r ON r.id = sp.mt_router_id
WHERE r.default_mt_engine_id IS NOT NULL OR r.default_ai_engine_id IS NOT NULL;

INSERT INTO settings_mt_assignments (settings_profile_id, mt_engine_id, ai_engine_id, source_language, target_language)
SELECT sp.id, rr.mt_engine_id, rr.ai_engine_id, COALESCE(p.source_language, '*'), rr.target_lang
FROM settings_profiles sp
JOIN mt_router_rules rr ON rr.router_id = sp.mt_router_id
LEFT JOIN projects p ON p.settings_profile_id = sp.id;

ALTER TABLE settings_profiles DROP COLUMN mt_router_id;
DROP TABLE mt_router_rules;
DROP TABLE mt_routers;

-- +goose StatementEnd
