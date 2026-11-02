-- +goose Up
-- Every template names a segmentation profile. A template that names none
-- gets the newest built-in for its language, then for its base language,
-- then Default (also for a template without a language: the built-ins for any
-- language are several, and Default is the one to start with).
UPDATE settings_profiles sp
SET segmentation_profile_id = coalesce(
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE coalesce(t.source_language, '') <> '' AND b.source_language = lower(t.source_language) ORDER BY b.id DESC LIMIT 1),
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE coalesce(t.source_language, '') <> '' AND b.source_language = split_part(lower(t.source_language), '-', 1) ORDER BY b.id DESC LIMIT 1),
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE b.builtin_key LIKE 'builtin:default@%' ORDER BY b.id DESC LIMIT 1))
FROM templates t
WHERE t.settings_profile_id = sp.id AND sp.segmentation_profile_id IS NULL;

-- +goose Down
-- Nothing to undo: the profiles given stay valid.
SELECT 1;
