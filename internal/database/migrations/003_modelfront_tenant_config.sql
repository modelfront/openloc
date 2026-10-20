-- +goose Up
ALTER TABLE tenants ADD COLUMN modelfront_api_key text;
ALTER TABLE tenants ADD COLUMN modelfront_base_url text DEFAULT 'https://api.modelfront.com/v1';

-- +goose Down
ALTER TABLE tenants DROP COLUMN modelfront_api_key;
ALTER TABLE tenants DROP COLUMN modelfront_base_url;
