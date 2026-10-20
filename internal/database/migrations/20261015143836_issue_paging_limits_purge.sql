-- +goose Up
-- The issue lists page by last update, newest or oldest first: one index for
-- an org's list and one for the operators' list across orgs.
CREATE INDEX issues_tenant_updated_idx ON issues (tenant_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX issues_updated_idx ON issues (updated_at DESC, id DESC) WHERE deleted_at IS NULL;

-- The per-member comment limit counts a member's recent comments.
CREATE INDEX issue_comments_author_created_idx ON issue_comments (author_id, created_at) WHERE author_side = 'reporter';

-- Every deleted issue_attachments row queues its storage key here, whatever
-- deleted it (a tenant or user delete cascades through issues). An hourly
-- sweep deletes the queued files and then their queue rows, so a failed file
-- delete is retried on the next run.
CREATE TABLE issue_attachment_blob_purges (
    storage_key TEXT PRIMARY KEY,
    queued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION queue_issue_attachment_blob_purge() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO issue_attachment_blob_purges (storage_key)
    VALUES (OLD.storage_key)
    ON CONFLICT (storage_key) DO NOTHING;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER issue_attachments_queue_blob_purge
AFTER DELETE ON issue_attachments
FOR EACH ROW EXECUTE FUNCTION queue_issue_attachment_blob_purge();

-- +goose Down
DROP TRIGGER issue_attachments_queue_blob_purge ON issue_attachments;
DROP FUNCTION queue_issue_attachment_blob_purge();
DROP TABLE issue_attachment_blob_purges;
DROP INDEX issue_comments_author_created_idx;
DROP INDEX issues_updated_idx;
DROP INDEX issues_tenant_updated_idx;
