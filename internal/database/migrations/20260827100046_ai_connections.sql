-- +goose Up
-- Split "where we connect" from "what authenticates".
--
-- `base_url` has lived on the token since Azure support landed, which made
-- the endpoint a property of each individual key: two keys for the same Azure
-- resource had to repeat the URL, and rotating a key meant re-entering it. An
-- ai_connection owns {vendor, name, endpoint}; a token is now purely a
-- credential belonging to one connection.

CREATE TABLE ai_connections (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vendor_id  bigint NOT NULL REFERENCES ai_vendors(id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- Empty = the vendor's own public API. Same semantics base_url had on the
    -- token, normalised by ai.NormalizeBaseURL before it lands here.
    base_url   text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_connections_vendor ON ai_connections (vendor_id);
-- Deliberately NO UNIQUE (vendor_id, name): migration 083 dropped exactly that
-- constraint on the sibling tables because default names ("New connection")
-- collided on the second create. Names are labels here, not keys.

ALTER TABLE ai_vendor_tokens ADD COLUMN connection_id bigint REFERENCES ai_connections(id) ON DELETE RESTRICT;

-- Backfill: one connection per DISTINCT (vendor, endpoint) already in use, so a
-- tenant running two Azure resources through one vendor keeps both. Tokens that
-- shared an endpoint end up sharing a connection, which is the point.
INSERT INTO ai_connections (vendor_id, name, base_url)
SELECT DISTINCT ON (t.vendor_id, COALESCE(t.base_url, ''))
       t.vendor_id,
       -- NOT initcap(): it lowercases everything after the first letter and
       -- turns "openai" into "Openai". These are the names users read, and they
       -- must match ai.DisplayName.
       CASE
           WHEN COALESCE(t.base_url, '') = '' THEN vendor_label.name
           -- Name it after the host so two endpoints on one vendor are telling
           -- apart at a glance; regexp strips scheme and any path.
           ELSE vendor_label.name || ' — ' || regexp_replace(t.base_url, '^https?://([^/]+).*$', '\1')
       END,
       COALESCE(t.base_url, '')
FROM ai_vendor_tokens t
JOIN ai_vendors v ON v.id = t.vendor_id
CROSS JOIN LATERAL (SELECT CASE v.provider
    WHEN 'openai' THEN 'OpenAI'
    WHEN 'claude' THEN 'Claude'
    WHEN 'modelfront' THEN 'ModelFront'
    ELSE v.provider
END AS name) AS vendor_label
ORDER BY t.vendor_id, COALESCE(t.base_url, ''), t.id;

UPDATE ai_vendor_tokens t
SET connection_id = c.id
FROM ai_connections c
WHERE c.vendor_id = t.vendor_id AND c.base_url = COALESCE(t.base_url, '');

-- Every token now belongs to a connection; nothing may be created without one.
ALTER TABLE ai_vendor_tokens ALTER COLUMN connection_id SET NOT NULL;
CREATE INDEX idx_ai_vendor_tokens_connection ON ai_vendor_tokens (connection_id);

ALTER TABLE ai_vendor_tokens DROP COLUMN base_url;

-- +goose Down
ALTER TABLE ai_vendor_tokens ADD COLUMN base_url text;
UPDATE ai_vendor_tokens t
SET base_url = NULLIF(c.base_url, '')
FROM ai_connections c
WHERE c.id = t.connection_id;

DROP INDEX IF EXISTS idx_ai_vendor_tokens_connection;
ALTER TABLE ai_vendor_tokens DROP COLUMN connection_id;
DROP TABLE ai_connections;
