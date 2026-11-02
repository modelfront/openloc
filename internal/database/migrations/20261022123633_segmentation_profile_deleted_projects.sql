-- +goose Up
-- Every project names a segmentation profile. A project that names none gets
-- the newest built-in for its language, then for its base language, then
-- Default (also for a project without a language: the built-ins for any
-- language are several, and Default is the one to start with). After that, a project that names a deleted profile loses it: its
-- next upload asks for another profile.
UPDATE settings_profiles sp
SET segmentation_profile_id = coalesce(
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE p.source_language <> '' AND b.source_language = lower(p.source_language) ORDER BY b.id DESC LIMIT 1),
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE p.source_language <> '' AND b.source_language = split_part(lower(p.source_language), '-', 1) ORDER BY b.id DESC LIMIT 1),
    (SELECT b.id FROM segmentation_builtin_profiles b
     WHERE b.builtin_key LIKE 'builtin:default@%' ORDER BY b.id DESC LIMIT 1))
FROM projects p
WHERE p.settings_profile_id = sp.id AND sp.segmentation_profile_id IS NULL;

UPDATE settings_profiles sp
SET segmentation_profile_id = NULL
FROM segmentation_profiles o
WHERE o.id = sp.segmentation_profile_id AND o.deleted_at IS NOT NULL
  AND EXISTS (SELECT 1 FROM projects p WHERE p.settings_profile_id = sp.id);

-- +goose Down
-- Nothing to undo: the profiles given stay valid, and a cleared one cannot be
-- told apart from a project that had none.
SELECT 1;
