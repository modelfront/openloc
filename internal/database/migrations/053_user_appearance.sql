-- +goose Up
-- Per-user appearance preferences. Keyed by user_id (NOT tenant) so a
-- chosen palette follows the user across every org they belong to. `light_palette`
-- / `dark_palette` name the palette active in each mode (built-in id or a custom
-- palette's id); `custom_palettes` holds the user's own palettes as JSON.
CREATE TABLE user_appearance (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    light_palette   TEXT NOT NULL DEFAULT 'paper',
    dark_palette    TEXT NOT NULL DEFAULT 'dim',
    custom_palettes JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE user_appearance;
