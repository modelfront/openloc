-- +goose Up
-- +goose StatementBegin

-- Row grouping for the language-pairs editor (UI-confirmed design).
--
-- The editor lets an admin enter pairs as "rows", each row multi-selecting
-- sources × targets. The underlying data is still a flat set of (source,
-- target) pairs, but the UI wants to round-trip the row grouping so that two
-- rows sharing a source (e.g. de→en and de→fr) stay distinct components in the
-- editor and the member list, instead of merging when regrouped by source.
--
-- `row_group` records which editor row a pair came from. On read we group by
-- row_group to reconstruct the rows; on write the client sends each pair's row
-- index. Defaults to 0 so existing pairs collapse into a single first row
-- (harmless — a flat set has no rows, and it reconstructs as one group).
ALTER TABLE user_language_pairs
    ADD COLUMN row_group integer NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE user_language_pairs DROP COLUMN row_group;

-- +goose StatementEnd
