-- +goose Up
-- +goose StatementBegin
-- Notifies channel translation_holds with the job id when a change the job's
-- other editors can see commits: a hold taken, passed to another user, or
-- ended while still live. Renewals and the sweep of expired rows stay silent.
-- Every server process listens and wakes that job's open editors, which
-- refetch the holds. Postgres folds duplicate notices within a transaction.
CREATE FUNCTION translation_holds_notify() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM pg_notify('translation_holds', NEW.job_id::text);
    ELSIF TG_OP = 'UPDATE' THEN
        IF OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.expires_at <= now() THEN
            PERFORM pg_notify('translation_holds', NEW.job_id::text);
        END IF;
    ELSIF OLD.expires_at > now() THEN
        PERFORM pg_notify('translation_holds', OLD.job_id::text);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER translation_holds_notify
AFTER INSERT OR UPDATE OR DELETE ON translation_holds
FOR EACH ROW EXECUTE FUNCTION translation_holds_notify();

-- +goose Down
DROP TRIGGER IF EXISTS translation_holds_notify ON translation_holds;
DROP FUNCTION IF EXISTS translation_holds_notify();
