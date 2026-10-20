-- +goose Up
-- +goose StatementBegin

-- A draft project may still miss its name and languages; every other status
-- must be complete. The columns stay NOT NULL (drafts store '' / '{}') so the
-- generated Go types don't change — the constraint is the backstop behind the
-- handler-level validation on publish.
ALTER TABLE projects ADD CONSTRAINT projects_complete_unless_draft
    CHECK (status = 'draft'
           OR (name <> '' AND source_language <> '' AND cardinality(target_languages) > 0));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE projects DROP CONSTRAINT projects_complete_unless_draft;

-- +goose StatementEnd
