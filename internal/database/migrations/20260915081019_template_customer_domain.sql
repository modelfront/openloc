-- +goose Up
-- +goose StatementBegin
-- A template carries the project name, languages, customer and domain a
-- project made from it starts with. Deleting the customer unsets the link, as
-- on projects. Existing templates start with their own name as the project
-- name, the same default CreateTemplate applies to new ones.
ALTER TABLE templates
  ADD COLUMN project_name text,
  ADD COLUMN source_language text,
  ADD COLUMN target_languages text[] NOT NULL DEFAULT '{}',
  ADD COLUMN customer_id bigint REFERENCES customers(id) ON DELETE SET NULL,
  ADD COLUMN domain text;
UPDATE templates SET project_name = name;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE templates
  DROP COLUMN project_name,
  DROP COLUMN source_language,
  DROP COLUMN target_languages,
  DROP COLUMN customer_id,
  DROP COLUMN domain;
-- +goose StatementEnd
