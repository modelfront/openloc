-- +goose Up
-- +goose StatementBegin

-- Per-user language pair qualification (translator competency).
--
-- Used by workflow assignment to filter the assignee dropdown to users
-- qualified for the project's source/target pair.
--
-- Why composite primary key (user_id, source_lang, target_lang):
--   Natural dedup. A user "qualifies for en→fr" is one row; the same statement
--   repeated is the same row. No synthetic id needed.
--
-- Why ON DELETE CASCADE:
--   When a user is deleted, their pairs go with them. No orphans. Same
--   pattern as other user-scoped tables.
--
-- Why no `tenant_id` column:
--   Tenant isolation is inherited via `user_id → users.tenant_id`. EVERY
--   query that filters users by language pair MUST join through `users` and
--   add `WHERE users.tenant_id = $tenant_id`. Trusting `user_id` from a URL
--   path alone (without the tenant join) would be a cross-tenant leak.
--
-- Why index on (source_lang, target_lang):
--   Supports the hot query "list users qualified for this pair." Without it,
--   the workflow assignment dropdown would seq-scan the table.
--
-- Server normalizes language codes via langcode.Normalize() before
-- insert. This means EN/en/english collapse to en. Locale codes (en-US,
-- pt-BR) are preserved — they have meaning. Normalization does case + alias
-- collapse only.
--
-- Empty pairs = "not qualified for anything." Workflow assignment filter
-- excludes such users. Admin/owner roles bypass the filter.

CREATE TABLE user_language_pairs (
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_lang  text NOT NULL,
    target_lang  text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, source_lang, target_lang)
);

CREATE INDEX idx_user_language_pairs_pair
    ON user_language_pairs (source_lang, target_lang);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_user_language_pairs_pair;
DROP TABLE IF EXISTS user_language_pairs;

-- +goose StatementEnd
