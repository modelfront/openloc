-- +goose Up
-- Chart share links: a capability URL for ONE chart.
--
-- Distinct from dashboard visibility, deliberately. `dashboards.owner_id` says
-- who may BROWSE to a chart; a share token says "the holder of this URL may
-- read this one chart", which is a different question and gets a different
-- mechanism. The reads stay separate too — nothing ORs the token into the
-- owner predicate, so weakening one path cannot weaken the other.
--
-- The token is stored in the clear rather than hashed: unlike an API token this
-- is a link the owner has to be able to copy again, and it grants read of one
-- chart inside one tenant — strictly less than the DB it would be stolen from.
ALTER TABLE dashboard_charts
    ADD COLUMN share_token      text UNIQUE,
    ADD COLUMN share_created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN share_created_at timestamptz;

-- Revoking sets the token NULL, so the index is over the live ones only.
CREATE INDEX idx_dashboard_charts_share_token
    ON dashboard_charts (share_token) WHERE share_token IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_dashboard_charts_share_token;
ALTER TABLE dashboard_charts
    DROP COLUMN share_token,
    DROP COLUMN share_created_by,
    DROP COLUMN share_created_at;
