-- +goose Up
-- Per-project spell check dictionary — the persistence behind "Ignore all" in
-- the spell check review dialog.
--
-- Scoped to (project, language) rather than to the tenant: a term that's
-- correct in one client's brand voice (a brand name or product codename) is a
-- genuine misspelling in another's, and translators on the same project should
-- share the decision rather than each re-ignoring the same word.
--
-- `word` is matched CASE-SENSITIVELY on purpose. In German, `adresse` vs
-- `Adresse` is exactly the error a linguist is looking for; case-folding the
-- dictionary would suppress the real finding along with the false positive.
--
-- tenant_id is redundant against project_id but carried anyway so dictionary
-- reads can be tenant-scoped directly, per the project's tenant-isolation rule,
-- without a join through projects on every lookup.
CREATE TABLE spellcheck_dictionary (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id)  ON DELETE CASCADE,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    language   text   NOT NULL,
    word       text   NOT NULL,
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, language, word)
);

CREATE INDEX spellcheck_dictionary_lookup_idx
    ON spellcheck_dictionary (project_id, language);

-- +goose Down
DROP INDEX IF EXISTS spellcheck_dictionary_lookup_idx;
DROP TABLE IF EXISTS spellcheck_dictionary;
