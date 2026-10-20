-- +goose Up
-- +goose StatementBegin

-- How pre-translation walks several assigned TMs:
--   match_percent  exact pass over every TM in order, then fuzzy pass over
--                  every TM in order (the default)
--   tm_order       each TM in turn gets its exact pass and then its fuzzy pass
--                  before the next TM is consulted
-- TEXT + CHECK because the API accepts the value directly.
ALTER TABLE settings_profiles
  ADD COLUMN tm_lookup_order text NOT NULL DEFAULT 'match_percent'
    CHECK (tm_lookup_order IN ('match_percent', 'tm_order'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings_profiles DROP COLUMN IF EXISTS tm_lookup_order;
-- +goose StatementEnd
