-- +goose Up
-- +goose StatementBegin

-- Narrow translation_memory_pipeline_dedupe to source only
-- on the human path. The previous index (migration 014) keyed on
-- (tm_id, source_lang, target_lang, source_hash, target_hash) WHERE
-- created_by='human', so a translator re-confirming the same source with
-- a new target would bypass dedupe and stack three rows in the TM aside.
-- The intended behaviour is to
-- overwrite the active TU on re-confirm and snapshot the prior values into
-- a separate audit log — OpenLoc's translation_memory_history (migration
-- 022) is now wired into the pipeline write-back path for that purpose.
--
-- The TMX import path is unaffected: those rows have created_by ∈
-- {Trados, memoQ, SDL, ...} which sits outside the partial predicate.

-- Step 1: Snapshot existing duplicate losers' BEFORE state into
-- translation_memory_history, pick survivors (most-recent updated_at;
-- id tiebreaker), sum usage_count into the survivor, delete losers.
-- Runs against the OLD index shape so all duplicates still exist when
-- the cleanup CTE evaluates.
WITH ranked AS (
  SELECT id, tm_id, source_language, target_language, source_hash,
         source_text, target_text, source_rich_text, target_rich_text,
         usage_count, updated_at,
         ROW_NUMBER() OVER (
           PARTITION BY tm_id, source_language, target_language, source_hash
           ORDER BY updated_at DESC, id DESC
         ) AS rn,
         FIRST_VALUE(id) OVER (
           PARTITION BY tm_id, source_language, target_language, source_hash
           ORDER BY updated_at DESC, id DESC
         ) AS survivor_id,
         SUM(usage_count) OVER (
           PARTITION BY tm_id, source_language, target_language, source_hash
         ) AS group_usage_sum,
         COUNT(*) OVER (
           PARTITION BY tm_id, source_language, target_language, source_hash
         ) AS group_size
    FROM translation_memory
   WHERE created_by = 'human'
),
losers AS (SELECT * FROM ranked WHERE rn > 1),
history_writes AS (
  INSERT INTO translation_memory_history
    (entry_id, source_text, target_text, source_rich_text, target_rich_text,
     changed_by_user_id, change_reason)
  SELECT survivor_id, source_text, target_text, source_rich_text, target_rich_text,
         NULL, 'migration_dedupe'
    FROM losers
  RETURNING entry_id
),
usage_bumps AS (
  UPDATE translation_memory tm
     SET usage_count = r.group_usage_sum
    FROM ranked r
   WHERE tm.id = r.survivor_id
     AND r.rn = 1
     AND r.group_size > 1
  RETURNING tm.id
),
deletions AS (
  DELETE FROM translation_memory
   WHERE id IN (SELECT id FROM losers)
  RETURNING id
)
SELECT COUNT(*) FROM deletions;  -- forces CTE chain to evaluate

-- Step 2: swap the index. Concurrent writers block on the table lock the
-- migration tx holds, so no duplicate can sneak in between drop and create.
DROP INDEX IF EXISTS translation_memory_pipeline_dedupe;

CREATE UNIQUE INDEX translation_memory_pipeline_dedupe
  ON translation_memory (tm_id, source_language, target_language, source_hash)
  WHERE created_by = 'human';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS translation_memory_pipeline_dedupe;
CREATE UNIQUE INDEX translation_memory_pipeline_dedupe
  ON translation_memory (tm_id, source_language, target_language, source_hash, target_hash)
  WHERE created_by = 'human';
-- +goose StatementEnd
