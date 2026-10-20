-- +goose Up
-- A segment hold: the user who has a translation open in the CAT editor. While
-- it is live (expires_at in the future), other users' writes to the row are
-- refused and their editors show the segment read-only with the holder's name.
-- A lease: renewed while the editor keeps the segment open, deleted when it
-- closes, and left to expire when a tab disappears.
CREATE TABLE translation_holds (
    translation_id bigint PRIMARY KEY REFERENCES translations(id) ON DELETE CASCADE,
    tenant_id      bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    job_id         bigint NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    user_id        bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The browser tab that holds it: a tab holds one segment at a time, and
    -- releases only its own hold.
    tab_id         text NOT NULL,
    acquired_at    timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL
);

CREATE INDEX idx_translation_holds_job ON translation_holds (job_id, expires_at);
CREATE INDEX idx_translation_holds_tab ON translation_holds (user_id, tab_id);

-- +goose Down
DROP TABLE translation_holds;
