-- +goose Up
-- Language pairs belong to a membership: each organization sets and reads its
-- own pairs for a person, and removing the person from an organization removes
-- the pairs it set. A person's pairs are copied to each organization they are in.
ALTER TABLE user_language_pairs DROP CONSTRAINT user_language_pairs_pkey;
ALTER TABLE user_language_pairs ADD COLUMN tenant_id bigint;

INSERT INTO user_language_pairs (user_id, tenant_id, source_lang, target_lang, row_group, created_at)
SELECT p.user_id, m.tenant_id, p.source_lang, p.target_lang, p.row_group, p.created_at
FROM user_language_pairs p
JOIN memberships m ON m.user_id = p.user_id
WHERE p.tenant_id IS NULL;

DELETE FROM user_language_pairs WHERE tenant_id IS NULL;

ALTER TABLE user_language_pairs ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE user_language_pairs ADD PRIMARY KEY (tenant_id, user_id, source_lang, target_lang);
ALTER TABLE user_language_pairs ADD CONSTRAINT user_language_pairs_membership_fkey
    FOREIGN KEY (user_id, tenant_id) REFERENCES memberships (user_id, tenant_id) ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_user_language_pairs_pair;
CREATE INDEX idx_user_language_pairs_pair ON user_language_pairs (tenant_id, source_lang, target_lang);

-- +goose Down
-- A person's pairs become one set again: the union over their organizations,
-- each pair keeping its lowest row group.
ALTER TABLE user_language_pairs DROP CONSTRAINT user_language_pairs_membership_fkey;
ALTER TABLE user_language_pairs DROP CONSTRAINT user_language_pairs_pkey;
DROP INDEX IF EXISTS idx_user_language_pairs_pair;

DELETE FROM user_language_pairs p
USING user_language_pairs o
WHERE o.user_id = p.user_id AND o.source_lang = p.source_lang AND o.target_lang = p.target_lang
  AND (o.row_group, o.tenant_id) < (p.row_group, p.tenant_id);

ALTER TABLE user_language_pairs DROP COLUMN tenant_id;
ALTER TABLE user_language_pairs ADD PRIMARY KEY (user_id, source_lang, target_lang);
CREATE INDEX idx_user_language_pairs_pair ON user_language_pairs (source_lang, target_lang);
