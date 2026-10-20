-- +goose Up
-- The support team's side of an issue: who works on it, how urgent it is, free
-- tags, the issue it duplicates, and the reporter's confirmation of a fix.
-- Priority and tags are the support team's own: the reporter's org never
-- reads them.
ALTER TABLE issues
    ADD COLUMN assignee_id bigint REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN priority text CHECK (priority IN ('low', 'medium', 'high')),
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}',
    ADD COLUMN duplicate_of_id bigint REFERENCES issues(id) ON DELETE SET NULL,
    ADD COLUMN confirmed_at timestamptz,
    -- Where an imported issue came from (a sheet and row): a second import of
    -- the same source finds it and skips it.
    ADD COLUMN import_key text CHECK (char_length(import_key) BETWEEN 1 AND 200);

ALTER TABLE issues DROP CONSTRAINT issues_status_check;
ALTER TABLE issues ADD CONSTRAINT issues_status_check
    CHECK (status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix', 'duplicate'));
-- A duplicate's original is NULL only while a hard delete of the original
-- cascades; the delete then reopens the duplicate.
ALTER TABLE issues ADD CONSTRAINT issues_duplicate_check
    CHECK ((duplicate_of_id IS NULL OR status = 'duplicate') AND duplicate_of_id <> id);
ALTER TABLE issues ADD CONSTRAINT issues_confirmed_check
    CHECK (confirmed_at IS NULL OR status = 'resolved');

CREATE UNIQUE INDEX issues_import_key_idx ON issues (tenant_id, import_key) WHERE import_key IS NOT NULL;
CREATE INDEX issues_tags_idx ON issues USING gin (tags) WHERE deleted_at IS NULL;
-- Search matches any part of the title or the description.
CREATE INDEX issues_title_trgm_idx ON issues USING gin (title gin_trgm_ops) WHERE deleted_at IS NULL;
CREATE INDEX issues_description_trgm_idx ON issues USING gin (description gin_trgm_ops) WHERE deleted_at IS NULL;
-- The ON DELETE SET NULL of both new foreign keys looks rows up by them: a
-- deleted org's issues, a deleted user.
CREATE INDEX issues_duplicate_of_idx ON issues (duplicate_of_id) WHERE duplicate_of_id IS NOT NULL;
CREATE INDEX issues_assignee_idx ON issues (assignee_id) WHERE assignee_id IS NOT NULL;
-- The support team's accounts are looked up by email, compared case-insensitively.
CREATE INDEX users_lower_email_idx ON users (lower(email));

-- An entry is a comment, a status change, a change of one other field (field,
-- from_value, to_value), or a mix. Internal entries are the support team's:
-- their notes, and every priority, severity, area and tag change.
ALTER TABLE issue_comments
    ADD COLUMN internal boolean NOT NULL DEFAULT false,
    ADD COLUMN field text CHECK (field IN ('assignee', 'priority', 'severity', 'area', 'tags', 'duplicate', 'confirmed')),
    ADD COLUMN from_value text CHECK (char_length(from_value) <= 1000),
    ADD COLUMN to_value text CHECK (char_length(to_value) <= 1000);

ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_status_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_status_check
    CHECK (status IS NULL OR status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix', 'duplicate'));
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_field_check
    CHECK (field IS NULL OR internal = (field IN ('priority', 'severity', 'area', 'tags')));
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_side_check
    CHECK (NOT internal OR author_side = 'operator');

-- +goose Down
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_side_check;
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_field_check;
DELETE FROM issue_comments WHERE internal OR field IS NOT NULL AND body IS NULL AND status IS NULL;
UPDATE issue_comments SET status = 'resolved' WHERE status = 'duplicate';
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_status_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_status_check
    CHECK (status IS NULL OR status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix'));
ALTER TABLE issue_comments
    DROP COLUMN to_value,
    DROP COLUMN from_value,
    DROP COLUMN field,
    DROP COLUMN internal;

DROP INDEX users_lower_email_idx;
DROP INDEX issues_assignee_idx;
DROP INDEX issues_duplicate_of_idx;
DROP INDEX issues_description_trgm_idx;
DROP INDEX issues_title_trgm_idx;
DROP INDEX issues_tags_idx;
DROP INDEX issues_import_key_idx;
ALTER TABLE issues DROP CONSTRAINT issues_confirmed_check;
ALTER TABLE issues DROP CONSTRAINT issues_duplicate_check;
UPDATE issues SET status = 'resolved', duplicate_of_id = NULL WHERE status = 'duplicate';
ALTER TABLE issues DROP CONSTRAINT issues_status_check;
ALTER TABLE issues ADD CONSTRAINT issues_status_check
    CHECK (status IN ('open', 'in_progress', 'needs_info', 'resolved', 'wont_fix'));
ALTER TABLE issues
    DROP COLUMN import_key,
    DROP COLUMN confirmed_at,
    DROP COLUMN duplicate_of_id,
    DROP COLUMN tags,
    DROP COLUMN priority,
    DROP COLUMN assignee_id;
