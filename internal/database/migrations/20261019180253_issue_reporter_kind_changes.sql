-- +goose Up
-- The reporter's side changes an issue's kind too. Their kind change is an
-- entry the org sees; the support team's stays internal. The severity such a
-- change sets or clears is an internal entry written from the reporter's
-- side, so the support team keeps the severity's history.
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_field_check
    CHECK (field IS NULL OR internal = (field IN ('priority', 'severity', 'area', 'tags')
        OR (field = 'kind' AND author_side = 'operator')));
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_side_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_side_check
    CHECK (NOT internal OR author_side = 'operator' OR field = 'severity');

-- +goose Down
DELETE FROM issue_comments WHERE field IN ('kind', 'severity') AND author_side = 'reporter' AND body IS NULL AND status IS NULL;
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_side_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_side_check
    CHECK (NOT internal OR author_side = 'operator');
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_internal_field_check;
ALTER TABLE issue_comments ADD CONSTRAINT issue_comments_internal_field_check
    CHECK (field IS NULL OR internal = (field IN ('priority', 'severity', 'area', 'tags', 'kind')));
