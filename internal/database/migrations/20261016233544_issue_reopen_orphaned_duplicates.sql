-- +goose Up
-- A duplicate whose original goes is tracked nowhere, so it goes back to the
-- support team's queue: open, with no original, waiting on the support team,
-- with a status entry its org sees. reopen_issue_duplicates does that for the
-- given duplicates. The soft delete of an issue calls it for the issue's
-- duplicates; a hard delete (an org or an account closing, cascading through
-- issues) leaves them with a NULL original, and the trigger reopens those once
-- the delete statement is done.
-- +goose StatementBegin
CREATE FUNCTION reopen_issue_duplicates(ids bigint[]) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    WITH reopened AS (
        UPDATE issues SET status = 'open', duplicate_of_id = NULL, last_activity_by = 'reporter',
            resolved_at = NULL, updated_at = now()
        WHERE id = ANY(ids) AND status = 'duplicate' AND deleted_at IS NULL
        RETURNING id
    )
    INSERT INTO issue_comments (issue_id, author_side, status)
    SELECT id, 'operator', 'open' FROM reopened;
END;
$$;

CREATE FUNCTION reopen_orphaned_issue_duplicates() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM reopen_issue_duplicates(ARRAY(
        SELECT id FROM issues
        WHERE status = 'duplicate' AND duplicate_of_id IS NULL AND deleted_at IS NULL));
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE INDEX issues_orphaned_duplicates_idx ON issues (id)
    WHERE status = 'duplicate' AND duplicate_of_id IS NULL AND deleted_at IS NULL;

CREATE TRIGGER issues_reopen_orphaned_duplicates
AFTER DELETE ON issues
FOR EACH STATEMENT EXECUTE FUNCTION reopen_orphaned_issue_duplicates();

-- +goose Down
DROP TRIGGER issues_reopen_orphaned_duplicates ON issues;
DROP INDEX issues_orphaned_duplicates_idx;
DROP FUNCTION reopen_orphaned_issue_duplicates();
DROP FUNCTION reopen_issue_duplicates(bigint[]);
