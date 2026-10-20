-- +goose Up

-- =============================================================
-- Step 1: Create settings_profiles table (single source of truth)
-- =============================================================
CREATE TABLE settings_profiles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    fuzzy_threshold real NOT NULL DEFAULT 0.75,
    pre_translate_on_upload boolean NOT NULL DEFAULT false,
    qa_profile_id bigint REFERENCES qa_profiles(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_settings_profiles_tenant ON settings_profiles (tenant_id);

-- =============================================================
-- Step 2: Create unified assignment tables
-- =============================================================
CREATE TABLE settings_tm_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settings_profile_id bigint NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
    tm_id bigint NOT NULL REFERENCES tm_collections(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read_write',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (settings_profile_id, tm_id, source_language, target_language)
);
CREATE INDEX idx_settings_tm_lookup ON settings_tm_assignments (settings_profile_id, source_language, target_language);

CREATE TABLE settings_mt_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settings_profile_id bigint NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
    mt_engine_id bigint NOT NULL REFERENCES mt_engines(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    UNIQUE (settings_profile_id, source_language, target_language)
);
CREATE INDEX idx_settings_mt_lookup ON settings_mt_assignments (settings_profile_id, source_language, target_language);

CREATE TABLE settings_terminology_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settings_profile_id bigint NOT NULL REFERENCES settings_profiles(id) ON DELETE CASCADE,
    term_base_id bigint NOT NULL REFERENCES term_bases(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (settings_profile_id, term_base_id, source_language, target_language)
);
CREATE INDEX idx_settings_term_lookup ON settings_terminology_assignments (settings_profile_id, source_language, target_language);

-- =============================================================
-- Step 3: Add settings_profile_id FK to projects and templates (nullable for now)
-- =============================================================
ALTER TABLE projects ADD COLUMN settings_profile_id bigint REFERENCES settings_profiles(id);
ALTER TABLE templates ADD COLUMN settings_profile_id bigint REFERENCES settings_profiles(id);

-- =============================================================
-- Step 4: Migrate existing project data into settings_profiles
-- =============================================================

-- +goose StatementBegin
-- Create a settings_profile for each project and link it
DO $$
DECLARE
    r RECORD;
    profile_id bigint;
BEGIN
    FOR r IN SELECT id, tenant_id, fuzzy_threshold, pre_translate_on_upload, qa_profile_id FROM projects ORDER BY id LOOP
        INSERT INTO settings_profiles (tenant_id, fuzzy_threshold, pre_translate_on_upload, qa_profile_id)
        VALUES (r.tenant_id, r.fuzzy_threshold, r.pre_translate_on_upload, r.qa_profile_id)
        RETURNING id INTO profile_id;

        UPDATE projects SET settings_profile_id = profile_id WHERE id = r.id;
    END LOOP;
END $$;
-- +goose StatementEnd

-- Copy project TM assignments
INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, source_language, target_language, mode, priority)
SELECT p.settings_profile_id, a.tm_id, a.source_language, a.target_language, a.mode, a.priority
FROM project_tm_assignments a JOIN projects p ON p.id = a.project_id;

-- Copy project MT assignments
INSERT INTO settings_mt_assignments (settings_profile_id, mt_engine_id, source_language, target_language)
SELECT p.settings_profile_id, a.mt_engine_id, a.source_language, a.target_language
FROM project_mt_assignments a JOIN projects p ON p.id = a.project_id;

-- Copy project terminology assignments
INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, mode, priority)
SELECT p.settings_profile_id, a.term_base_id, a.source_language, a.target_language, a.mode, a.priority
FROM project_terminology_assignments a JOIN projects p ON p.id = a.project_id;

-- =============================================================
-- Step 5: Migrate existing template data into settings_profiles
-- =============================================================

-- +goose StatementBegin
DO $$
DECLARE
    r RECORD;
    profile_id bigint;
BEGIN
    FOR r IN SELECT id, tenant_id, fuzzy_threshold, pre_translate_on_upload, qa_profile_id FROM templates ORDER BY id LOOP
        INSERT INTO settings_profiles (tenant_id, fuzzy_threshold, pre_translate_on_upload, qa_profile_id)
        VALUES (r.tenant_id, r.fuzzy_threshold, r.pre_translate_on_upload, r.qa_profile_id)
        RETURNING id INTO profile_id;

        UPDATE templates SET settings_profile_id = profile_id WHERE id = r.id;
    END LOOP;
END $$;
-- +goose StatementEnd

-- Copy template TM assignments
INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, source_language, target_language, mode, priority)
SELECT t.settings_profile_id, a.tm_id, a.source_language, a.target_language, a.mode, a.priority
FROM template_tm_assignments a JOIN templates t ON t.id = a.template_id;

-- Copy template MT assignments
INSERT INTO settings_mt_assignments (settings_profile_id, mt_engine_id, source_language, target_language)
SELECT t.settings_profile_id, a.mt_engine_id, a.source_language, a.target_language
FROM template_mt_assignments a JOIN templates t ON t.id = a.template_id;

-- Copy template terminology assignments
INSERT INTO settings_terminology_assignments (settings_profile_id, term_base_id, source_language, target_language, mode, priority)
SELECT t.settings_profile_id, a.term_base_id, a.source_language, a.target_language, a.mode, a.priority
FROM template_terminology_assignments a JOIN templates t ON t.id = a.template_id;

-- =============================================================
-- Step 6: Migrate workflow_steps to use settings_profile_id
-- =============================================================
ALTER TABLE workflow_steps ADD COLUMN settings_profile_id bigint REFERENCES settings_profiles(id) ON DELETE CASCADE;

-- Link project-scoped workflow steps
UPDATE workflow_steps ws SET settings_profile_id = p.settings_profile_id
FROM projects p WHERE ws.project_id = p.id AND ws.project_id IS NOT NULL;

-- Link template-scoped workflow steps
UPDATE workflow_steps ws SET settings_profile_id = t.settings_profile_id
FROM templates t WHERE ws.template_id = t.id AND ws.template_id IS NOT NULL;

-- =============================================================
-- Step 7: Make settings_profile_id NOT NULL, drop old columns/tables
-- =============================================================
ALTER TABLE projects ALTER COLUMN settings_profile_id SET NOT NULL;
ALTER TABLE templates ALTER COLUMN settings_profile_id SET NOT NULL;

-- Drop old scalar settings from projects
ALTER TABLE projects DROP COLUMN fuzzy_threshold;
ALTER TABLE projects DROP COLUMN pre_translate_on_upload;
ALTER TABLE projects DROP COLUMN qa_profile_id;

-- Drop old scalar settings from templates
ALTER TABLE templates DROP COLUMN fuzzy_threshold;
ALTER TABLE templates DROP COLUMN pre_translate_on_upload;
ALTER TABLE templates DROP COLUMN qa_profile_id;

-- Drop old workflow_steps FKs
ALTER TABLE workflow_steps DROP COLUMN project_id;
ALTER TABLE workflow_steps DROP COLUMN template_id;
ALTER TABLE workflow_steps ALTER COLUMN settings_profile_id SET NOT NULL;
CREATE INDEX idx_workflow_steps_profile ON workflow_steps (settings_profile_id, step_order);

-- Drop old assignment tables
DROP TABLE project_tm_assignments;
DROP TABLE project_mt_assignments;
DROP TABLE project_terminology_assignments;
DROP TABLE template_tm_assignments;
DROP TABLE template_mt_assignments;
DROP TABLE template_terminology_assignments;

-- +goose Down

-- Recreate old assignment tables
CREATE TABLE template_tm_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    template_id bigint NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    tm_id bigint NOT NULL REFERENCES tm_collections(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read_write',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (template_id, tm_id, source_language, target_language)
);
CREATE TABLE template_mt_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    template_id bigint NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    mt_engine_id bigint NOT NULL REFERENCES mt_engines(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    UNIQUE (template_id, source_language, target_language)
);
CREATE TABLE template_terminology_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    template_id bigint NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    term_base_id bigint NOT NULL REFERENCES term_bases(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (template_id, term_base_id, source_language, target_language)
);
CREATE TABLE project_tm_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tm_id bigint NOT NULL REFERENCES tm_collections(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read_write',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (project_id, tm_id, source_language, target_language)
);
CREATE TABLE project_mt_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    mt_engine_id bigint NOT NULL REFERENCES mt_engines(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    UNIQUE (project_id, source_language, target_language)
);
CREATE TABLE project_terminology_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    term_base_id bigint NOT NULL REFERENCES term_bases(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    mode text NOT NULL DEFAULT 'read',
    priority integer NOT NULL DEFAULT 0,
    UNIQUE (project_id, term_base_id, source_language, target_language)
);

-- Restore workflow_steps columns
ALTER TABLE workflow_steps ADD COLUMN template_id bigint REFERENCES templates(id) ON DELETE CASCADE;
ALTER TABLE workflow_steps ADD COLUMN project_id bigint REFERENCES projects(id) ON DELETE CASCADE;

-- Restore scalar settings on templates and projects
ALTER TABLE templates ADD COLUMN fuzzy_threshold real NOT NULL DEFAULT 0.75;
ALTER TABLE templates ADD COLUMN pre_translate_on_upload boolean NOT NULL DEFAULT false;
ALTER TABLE templates ADD COLUMN qa_profile_id bigint REFERENCES qa_profiles(id) ON DELETE SET NULL;
ALTER TABLE projects ADD COLUMN fuzzy_threshold real NOT NULL DEFAULT 0.75;
ALTER TABLE projects ADD COLUMN pre_translate_on_upload boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN qa_profile_id bigint REFERENCES qa_profiles(id) ON DELETE SET NULL;

-- Drop unified tables
ALTER TABLE projects DROP COLUMN settings_profile_id;
ALTER TABLE templates DROP COLUMN settings_profile_id;
DROP INDEX idx_workflow_steps_profile;
ALTER TABLE workflow_steps DROP COLUMN settings_profile_id;
DROP TABLE settings_terminology_assignments;
DROP TABLE settings_mt_assignments;
DROP TABLE settings_tm_assignments;
DROP TABLE settings_profiles;
