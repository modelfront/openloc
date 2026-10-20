-- +goose Up
-- The support team changes an issue's kind as part of triage, so a kind
-- change is an internal history entry, like severity and area.
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_field_check
    CHECK (field IN ('assignee', 'priority', 'severity', 'area', 'tags', 'duplicate', 'confirmed', 'kind'));
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_field_check
    CHECK (field IS NULL OR internal = (field IN ('priority', 'severity', 'area', 'tags', 'kind')));

-- +goose Down
DELETE FROM issue_comments WHERE field = 'kind';
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_field_check
    CHECK (field IS NULL OR internal = (field IN ('priority', 'severity', 'area', 'tags')));
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_field_check
    CHECK (field IN ('assignee', 'priority', 'severity', 'area', 'tags', 'duplicate', 'confirmed'));
