-- +goose Up
-- Split the pre-translate-on-upload setting into two independent flags:
--   pre_translate_on_upload  → MT on upload (column kept for back-compat,
--                              semantic narrowed from "everything" to "MT")
--   tm_on_upload (NEW)       → TM lookup on upload
--
-- Dependency rule (originally enforced at app + DB level): tm_on_upload may
-- only be true when pre_translate_on_upload is also true. Running TM-only on
-- upload (no MT fallback) leaves most segments untranslated and was thought
-- not useful as automatic behavior.
--
-- NOTE (2026-05-15): the CHECK constraint added below is DROPPED in migration
-- 034_drop_tm_implies_mt.sql: the two flags are independent. The current schema permits all
-- four combos. Keep this section for
-- historical accuracy of the schema timeline.
--
-- Templates share the same settings_profiles row via settings_profile_id, so
-- only that table needs the new column.
--
-- Backfill: existing rows where pre_translate=true set tm_on_upload=true to
-- preserve the combined behavior; pre_translate=false rows get tm_on_upload=false.

ALTER TABLE settings_profiles
    ADD COLUMN tm_on_upload BOOLEAN NOT NULL DEFAULT true;

UPDATE settings_profiles SET tm_on_upload = pre_translate_on_upload;

ALTER TABLE settings_profiles
    ADD CONSTRAINT settings_profiles_tm_implies_mt
    CHECK (NOT tm_on_upload OR pre_translate_on_upload);

-- +goose Down
ALTER TABLE settings_profiles DROP CONSTRAINT settings_profiles_tm_implies_mt;
ALTER TABLE settings_profiles DROP COLUMN tm_on_upload;
