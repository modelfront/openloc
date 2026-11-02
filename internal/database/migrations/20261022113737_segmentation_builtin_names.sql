-- +goose Up
-- The any-language built-in is "Default" and the technical one "Conservative",
-- as in their catalog files.
UPDATE segmentation_builtin_profiles SET name = 'Default' WHERE builtin_key = 'builtin:default@1';
UPDATE segmentation_builtin_profiles SET name = 'Conservative' WHERE builtin_key = 'builtin:conservative@1';

-- +goose Down
UPDATE segmentation_builtin_profiles SET name = 'Any language' WHERE builtin_key = 'builtin:default@1';
UPDATE segmentation_builtin_profiles SET name = 'Conservative (technical)' WHERE builtin_key = 'builtin:conservative@1';
