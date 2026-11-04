-- +goose Up
-- +goose StatementBegin
-- Notifies channel ai_catalog_forget with "<tenant id>|<provider>" when the
-- keys a vendor's model list is fetched with change: a key added, removed,
-- replaced, moved to another connection or chosen for chat, or a connection's
-- endpoint changed. Every server process listens and forgets that pair's
-- cached model list. A vendor deleted with its keys has nothing left to list,
-- so the lookup finds no row and nothing is sent.
CREATE FUNCTION ai_catalog_forget_notify() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    vid bigint;
    pair text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        vid := OLD.vendor_id;
    ELSIF TG_OP = 'INSERT' THEN
        vid := NEW.vendor_id;
    ELSIF TG_TABLE_NAME = 'ai_vendor_tokens' THEN
        IF OLD.api_key_encrypted IS NOT DISTINCT FROM NEW.api_key_encrypted
           AND OLD.connection_id IS NOT DISTINCT FROM NEW.connection_id
           AND OLD.use_for_chat IS NOT DISTINCT FROM NEW.use_for_chat THEN
            RETURN NULL;
        END IF;
        vid := NEW.vendor_id;
    ELSE
        IF OLD.base_url IS NOT DISTINCT FROM NEW.base_url THEN
            RETURN NULL;
        END IF;
        vid := NEW.vendor_id;
    END IF;
    SELECT v.tenant_id::text || '|' || v.provider INTO pair FROM ai_vendors v WHERE v.id = vid;
    IF pair IS NOT NULL THEN
        PERFORM pg_notify('ai_catalog_forget', pair);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER ai_vendor_tokens_catalog_forget
AFTER INSERT OR UPDATE OR DELETE ON ai_vendor_tokens
FOR EACH ROW EXECUTE FUNCTION ai_catalog_forget_notify();

CREATE TRIGGER ai_connections_catalog_forget
AFTER UPDATE ON ai_connections
FOR EACH ROW EXECUTE FUNCTION ai_catalog_forget_notify();

-- +goose Down
DROP TRIGGER IF EXISTS ai_connections_catalog_forget ON ai_connections;
DROP TRIGGER IF EXISTS ai_vendor_tokens_catalog_forget ON ai_vendor_tokens;
DROP FUNCTION IF EXISTS ai_catalog_forget_notify();
