-- +goose Up
-- +goose StatementBegin

-- Write on a term-base assignment decides which term bases the editor's term
-- quick-add adds terms to. A row created with Read only keeps quick-add
-- working: it gets Write too, and a row created without a mode gets both, as
-- the settings page creates it.

UPDATE settings_terminology_assignments SET mode = 'read_write' WHERE mode = 'read';

ALTER TABLE settings_terminology_assignments ALTER COLUMN mode SET DEFAULT 'read_write';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The rows the Up ticked Write on cannot be told apart from rows that had it,
-- so only the default goes back.
ALTER TABLE settings_terminology_assignments ALTER COLUMN mode SET DEFAULT 'read';

-- +goose StatementEnd
