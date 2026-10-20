-- +goose Up
-- Purge of assistant uploads. Every deleted chat_uploads
-- row queues its storage key here, whatever deleted it: the hourly expiry
-- sweep, a tenant delete or a user delete (both cascade). The same sweep
-- deletes the queued blobs and then their queue rows, so a failed blob delete
-- is retried on the next run.
CREATE INDEX chat_uploads_expires_at_idx ON chat_uploads (expires_at);

CREATE TABLE chat_upload_blob_purges (
    storage_key TEXT PRIMARY KEY,
    queued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION queue_chat_upload_blob_purge() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO chat_upload_blob_purges (storage_key)
    VALUES (OLD.storage_key)
    ON CONFLICT (storage_key) DO NOTHING;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER chat_uploads_queue_blob_purge
AFTER DELETE ON chat_uploads
FOR EACH ROW EXECUTE FUNCTION queue_chat_upload_blob_purge();

-- +goose Down
DROP TRIGGER chat_uploads_queue_blob_purge ON chat_uploads;
DROP FUNCTION queue_chat_upload_blob_purge();
DROP TABLE chat_upload_blob_purges;
DROP INDEX chat_uploads_expires_at_idx;
