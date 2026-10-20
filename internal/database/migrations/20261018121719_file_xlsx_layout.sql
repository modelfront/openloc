-- +goose Up
-- +goose StatementBegin

-- The uploader's column layout of an Excel workbook without language codes in
-- row 1 (parser.XLSXLayout: the sheets, their header row and the columns to
-- translate). NULL reads the workbook by its row 1.
ALTER TABLE files ADD COLUMN xlsx_layout jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE files DROP COLUMN xlsx_layout;

-- +goose StatementEnd
