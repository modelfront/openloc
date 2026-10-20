-- +goose Up
ALTER TABLE tenants ADD COLUMN open_registration BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tenants DROP COLUMN IF EXISTS open_registration;
