-- name: GetUserAppearance :one
SELECT user_id, light_palette, dark_palette, custom_palettes, created_at, updated_at
FROM user_appearance
WHERE user_id = $1;

-- name: UpsertUserAppearance :one
INSERT INTO user_appearance (user_id, light_palette, dark_palette, custom_palettes, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id) DO UPDATE SET
    light_palette   = EXCLUDED.light_palette,
    dark_palette    = EXCLUDED.dark_palette,
    custom_palettes = EXCLUDED.custom_palettes,
    updated_at      = now()
RETURNING user_id, light_palette, dark_palette, custom_palettes, created_at, updated_at;
