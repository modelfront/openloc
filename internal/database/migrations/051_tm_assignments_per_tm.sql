-- +goose Up
-- TM assignments become per-TM. A TM now declares its own
-- source_language + target_languages[] (migration 050), so the per-assignment
-- source/target columns are redundant — the pipeline derives a TM's language
-- coverage from tm_collections at lookup time.

-- Collapse existing per-(source,target) rows to one row per (profile, tm):
--   mode:     widen to 'read_write' if the TM had read_write on any pair, or had
--             both a 'read' and a 'write' pair; otherwise the single mode present.
--   priority: the minimum across the collapsed pair-rows.
CREATE TEMP TABLE _tm_collapsed AS
SELECT settings_profile_id,
       tm_id,
       CASE
         WHEN bool_or(mode = 'read_write')
           OR (bool_or(mode = 'read') AND bool_or(mode = 'write')) THEN 'read_write'
         ELSE min(mode)
       END AS mode,
       min(priority) AS priority
FROM settings_tm_assignments
GROUP BY settings_profile_id, tm_id;

DELETE FROM settings_tm_assignments;

-- Dropping the language columns cascades away the old
-- UNIQUE(profile, tm, source, target) and idx_settings_tm_lookup, which both
-- depend on these columns.
ALTER TABLE settings_tm_assignments
  DROP COLUMN source_language,
  DROP COLUMN target_language;

ALTER TABLE settings_tm_assignments
  ADD CONSTRAINT settings_tm_assignments_profile_tm_key UNIQUE (settings_profile_id, tm_id);

CREATE INDEX idx_settings_tm_lookup ON settings_tm_assignments (settings_profile_id);

-- id is GENERATED ALWAYS AS IDENTITY — omit it so new ids are assigned.
INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, mode, priority)
SELECT settings_profile_id, tm_id, mode, priority FROM _tm_collapsed;

DROP TABLE _tm_collapsed;

-- +goose Down
-- Best-effort inverse: re-add the language columns and reconstruct one row per
-- the TM's declared target language (source = the TM's declared source). TMs
-- with NULL declared languages collapse to a single ('', '') row.
CREATE TEMP TABLE _tm_expand AS
SELECT sta.settings_profile_id,
       sta.tm_id,
       COALESCE(tc.source_language, '') AS source_language,
       COALESCE(t.target, '')          AS target_language,
       sta.mode,
       sta.priority
FROM settings_tm_assignments sta
JOIN tm_collections tc ON tc.id = sta.tm_id
LEFT JOIN LATERAL unnest(COALESCE(NULLIF(tc.target_languages, '{}'), ARRAY[''])) AS t(target) ON true;

DELETE FROM settings_tm_assignments;

ALTER TABLE settings_tm_assignments DROP CONSTRAINT settings_tm_assignments_profile_tm_key;
DROP INDEX idx_settings_tm_lookup;

ALTER TABLE settings_tm_assignments
  ADD COLUMN source_language text NOT NULL DEFAULT '',
  ADD COLUMN target_language text NOT NULL DEFAULT '';

ALTER TABLE settings_tm_assignments
  ADD CONSTRAINT settings_tm_assignments_profile_tm_src_tgt_key
  UNIQUE (settings_profile_id, tm_id, source_language, target_language);
CREATE INDEX idx_settings_tm_lookup ON settings_tm_assignments (settings_profile_id, source_language, target_language);

INSERT INTO settings_tm_assignments (settings_profile_id, tm_id, source_language, target_language, mode, priority)
SELECT settings_profile_id, tm_id, source_language, target_language, mode, priority FROM _tm_expand;

ALTER TABLE settings_tm_assignments
  ALTER COLUMN source_language DROP DEFAULT,
  ALTER COLUMN target_language DROP DEFAULT;

DROP TABLE _tm_expand;
