-- +goose Up
-- Issues: a user reports a problem to the deployment's platform operators and
-- follows it here. Each issue has a conversation (issue_comments) that also
-- records status changes, and files attached to the issue or to a comment.
CREATE TABLE issues (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    reporter_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('bug', 'enhancement', 'feature_request')),
    -- Bugs only: how badly the problem gets in the way.
    severity text CHECK (severity IN ('minor', 'major', 'critical', 'blocker')),
    area text NOT NULL DEFAULT 'other' CHECK (area IN (
        'editor', 'tm', 'terminology', 'mt', 'ai_engines', 'qa', 'workflow',
        'project_settings', 'general_settings', 'dashboards', 'integrations', 'api', 'other')),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 10000),
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix')),
    context jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Who wrote or acted last.
    last_activity_by text NOT NULL DEFAULT 'reporter'
        CHECK (last_activity_by IN ('reporter', 'operator')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    deleted_at timestamptz,
    CHECK ((kind = 'bug') = (severity IS NOT NULL))
);

CREATE INDEX issues_reporter_idx ON issues (tenant_id, reporter_id, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX issues_status_idx ON issues (status, id DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE issue_comments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    issue_id bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    -- NULL once the author's account is deleted; the conversation stays.
    author_id bigint REFERENCES users(id) ON DELETE SET NULL,
    author_side text NOT NULL CHECK (author_side IN ('reporter', 'operator')),
    -- A comment without text carries attachments (checked by the app).
    body text CHECK (body IS NULL OR char_length(body) BETWEEN 1 AND 10000),
    -- The status this entry set, when it changed the status.
    status text CHECK (status IS NULL OR status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix')),
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX issue_comments_issue_idx ON issue_comments (issue_id, created_at, id)
    WHERE deleted_at IS NULL;

-- Files attached to an issue (comment_id NULL) or to one of its comments.
CREATE TABLE issue_attachments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    issue_id bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    comment_id bigint REFERENCES issue_comments(id) ON DELETE CASCADE,
    filename text NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255),
    content_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    storage_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX issue_attachments_issue_idx ON issue_attachments (issue_id, id);

-- The reporter's email on an operator comment or status change; the bell row
-- is always written.
ALTER TABLE notification_preferences
    ADD COLUMN email_issue_updates boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE notification_preferences DROP COLUMN email_issue_updates;
DROP TABLE issue_attachments;
DROP TABLE issue_comments;
DROP TABLE issues;
