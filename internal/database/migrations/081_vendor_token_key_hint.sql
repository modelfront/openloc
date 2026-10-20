-- Store a short, non-secret hint (the key's last 4 characters) per vendor token
-- so the token edit UI can show which key is stored without ever revealing it.
-- +goose Up
ALTER TABLE ai_vendor_tokens ADD COLUMN key_last4 text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ai_vendor_tokens DROP COLUMN key_last4;
