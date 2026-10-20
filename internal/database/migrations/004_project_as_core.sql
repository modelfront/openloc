-- +goose Up

-- =============================================================
-- Phase 1: Project-level settings columns
-- =============================================================
ALTER TABLE projects ADD COLUMN fuzzy_threshold real NOT NULL DEFAULT 0.75;
ALTER TABLE projects ADD COLUMN pre_translate_on_upload boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN qa_profile_id bigint REFERENCES qa_profiles(id) ON DELETE SET NULL;

-- =============================================================
-- Phase 2: Project assignment tables (mirror template_* tables)
-- =============================================================

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
CREATE INDEX idx_project_tm_lookup ON project_tm_assignments (project_id, source_language, target_language);

CREATE TABLE project_mt_assignments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    mt_engine_id bigint NOT NULL REFERENCES mt_engines(id) ON DELETE CASCADE,
    source_language text NOT NULL,
    target_language text NOT NULL,
    UNIQUE (project_id, source_language, target_language)
);
CREATE INDEX idx_project_mt_lookup ON project_mt_assignments (project_id, source_language, target_language);

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
CREATE INDEX idx_project_term_lookup ON project_terminology_assignments (project_id, source_language, target_language);

-- =============================================================
-- Phase 3: Allow workflow_steps to belong to project OR template
-- =============================================================
ALTER TABLE workflow_steps ALTER COLUMN template_id DROP NOT NULL;
ALTER TABLE workflow_steps ADD COLUMN project_id bigint REFERENCES projects(id) ON DELETE CASCADE;
CREATE INDEX idx_workflow_steps_project ON workflow_steps (project_id, step_order) WHERE project_id IS NOT NULL;

-- =============================================================
-- Phase 4: Data migration — copy template settings into projects
-- =============================================================
UPDATE projects p SET
    fuzzy_threshold = t.fuzzy_threshold,
    pre_translate_on_upload = t.pre_translate_on_upload,
    qa_profile_id = t.qa_profile_id
FROM templates t
WHERE p.template_id = t.id;

-- Copy TM assignments
INSERT INTO project_tm_assignments (project_id, tm_id, source_language, target_language, mode, priority)
SELECT p.id, tta.tm_id, tta.source_language, tta.target_language, tta.mode, tta.priority
FROM template_tm_assignments tta
JOIN projects p ON p.template_id = tta.template_id;

-- Copy MT assignments
INSERT INTO project_mt_assignments (project_id, mt_engine_id, source_language, target_language)
SELECT p.id, tma.mt_engine_id, tma.source_language, tma.target_language
FROM template_mt_assignments tma
JOIN projects p ON p.template_id = tma.template_id;

-- Copy terminology assignments
INSERT INTO project_terminology_assignments (project_id, term_base_id, source_language, target_language, mode, priority)
SELECT p.id, tta.term_base_id, tta.source_language, tta.target_language, tta.mode, tta.priority
FROM template_terminology_assignments tta
JOIN projects p ON p.template_id = tta.template_id;

-- Copy workflow steps (project-scoped copies; existing job_step_assignments still point to template steps)
INSERT INTO workflow_steps (project_id, name, step_order, role_required, default_assignee)
SELECT p.id, ws.name, ws.step_order, ws.role_required, ws.default_assignee
FROM workflow_steps ws
JOIN projects p ON p.template_id = ws.template_id
WHERE ws.template_id IS NOT NULL;

-- +goose Down
-- Remove project workflow steps (those with project_id set)
DELETE FROM workflow_steps WHERE project_id IS NOT NULL;
ALTER TABLE workflow_steps DROP COLUMN project_id;
ALTER TABLE workflow_steps ALTER COLUMN template_id SET NOT NULL;

DROP TABLE project_terminology_assignments;
DROP TABLE project_mt_assignments;
DROP TABLE project_tm_assignments;

ALTER TABLE projects DROP COLUMN qa_profile_id;
ALTER TABLE projects DROP COLUMN pre_translate_on_upload;
ALTER TABLE projects DROP COLUMN fuzzy_threshold;
