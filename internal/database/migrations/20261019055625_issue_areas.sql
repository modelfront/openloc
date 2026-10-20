-- +goose Up
-- Four more areas an issue can be about: projects, tasks, the AI assistant
-- (chat) and the MCP server.
ALTER TABLE issues DROP CONSTRAINT issues_area_check;
ALTER TABLE issues ADD CONSTRAINT issues_area_check CHECK (area IN (
    'editor', 'tm', 'terminology', 'mt', 'ai_engines', 'qa', 'workflow',
    'project_settings', 'general_settings', 'dashboards', 'integrations', 'api',
    'projects', 'tasks', 'chat', 'mcp', 'other'));

-- +goose Down
UPDATE issue_comments SET from_value = 'other' WHERE field = 'area' AND from_value IN ('projects', 'tasks', 'chat', 'mcp');
UPDATE issue_comments SET to_value = 'other' WHERE field = 'area' AND to_value IN ('projects', 'tasks', 'chat', 'mcp');
UPDATE issues SET area = 'other' WHERE area IN ('projects', 'tasks', 'chat', 'mcp');
ALTER TABLE issues DROP CONSTRAINT issues_area_check;
ALTER TABLE issues ADD CONSTRAINT issues_area_check CHECK (area IN (
    'editor', 'tm', 'terminology', 'mt', 'ai_engines', 'qa', 'workflow',
    'project_settings', 'general_settings', 'dashboards', 'integrations', 'api', 'other'));
