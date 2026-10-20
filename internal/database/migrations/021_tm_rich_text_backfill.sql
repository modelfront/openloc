-- +goose Up
-- +goose StatementBegin

-- Backfill source_rich_text + target_rich_text on existing translation_memory
-- rows that were imported BEFORE migration 020. Pre-020, the TMX importer
-- stuffed the raw <seg> InnerXML (including XLIFF inline tags) into
-- source_text / target_text. After 020 those columns are supposed to hold
-- only PLAIN text (with markup stripped), and rich_text holds the tag-
-- bearing form. Rows imported pre-020 have rich_text = NULL even when
-- source_text contains markup → the new <SegmentText> display layer falls
-- back to plain rendering and the user STILL sees raw `<bpt>...</bpt>`
-- markup for those old rows.
--
-- This migration moves the markup from plain → rich and strips the plain
-- column. Idempotent: only touches rows where rich_text is NULL AND text
-- contains XLIFF tags (per the regex below — same 8-element inline-tag set
-- that `internal/tmx/tmx.go` recognizes).
--
-- IMPORTANT: source_hash + target_hash get recomputed because the plain text
-- changed. The dedup partial unique index (`created_by = 'human'` from
-- migration 014) uses these hashes — so any post-backfill duplicate (from
-- two pre-020 rows that differed only in markup positions) would now collide.
-- That's the right behavior: the same translation pair should only have one
-- row regardless of how the markup was originally laid out. The DELETE step
-- below removes such duplicates BEFORE the rich_text move so the index
-- doesn't reject the update.
--
-- Pattern matches:
--   <bpt id="1">...</bpt>, <ept id="1">...</ept>, <ph id="1">...</ph>,
--   <it id="1">...</it>, <g id="1">...</g>, <hi id="1">...</hi>,
--   <sub id="1">...</sub>  (paired tags, content also dropped)
--   <bpt id="1"/>, <x/>, etc.  (self-closing / open)
--
-- We do this in two passes for clarity and so a partial failure (network
-- blip during the data move) leaves the table in a consistent state:
-- pass 1 strips, pass 2 promotes.

-- Pass 0: dedupe — collapse any (tm_id, source_lang, target_lang, post-strip
-- source_text, post-strip target_text) groups down to the first row, BEFORE
-- the partial-unique-index-on-human re-check happens. Only affects human-
-- created rows (TMX-imported rows are outside the partial index).
WITH stripped AS (
  SELECT
    id,
    tm_id, source_language, target_language,
    -- Same regex set as internal/tmx/tmx.go's two-pass stripper, fused into
    -- one regexp_replace call (PG regex supports backrefs so we CAN do it in
    -- one pattern — Go's RE2 cannot).
    regexp_replace(
      regexp_replace(source_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    ) AS plain_src,
    regexp_replace(
      regexp_replace(target_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    ) AS plain_tgt,
    created_by
  FROM translation_memory
  WHERE source_rich_text IS NULL
    AND (source_text ~ '<(bpt|ept|ph|it|x|g|hi|sub)\y' OR target_text ~ '<(bpt|ept|ph|it|x|g|hi|sub)\y')
),
ranked AS (
  SELECT id,
         row_number() OVER (PARTITION BY tm_id, source_language, target_language, plain_src, plain_tgt
                            ORDER BY id) AS rn
  FROM stripped
  WHERE created_by = 'human'  -- only human rows participate in the partial unique index
)
DELETE FROM translation_memory
WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

-- Pass 1+2 fused: move markup from plain text to rich_text + recompute hashes.
-- WHERE clause skips rows that don't need it (already have rich_text OR no markup).
UPDATE translation_memory
SET source_rich_text = source_text,
    target_rich_text = target_text,
    source_text = regexp_replace(
      regexp_replace(source_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    ),
    target_text = regexp_replace(
      regexp_replace(target_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    ),
    source_hash = md5(regexp_replace(
      regexp_replace(source_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    )),
    target_hash = md5(regexp_replace(
      regexp_replace(target_text,
        '<(bpt|ept|ph|it|g|hi|sub)\y[^>]*>.*?</\1>', '', 'g'),
      '</?(bpt|ept|ph|it|x|g|hi|sub)\y[^>]*/?>', '', 'g'
    ))
WHERE source_rich_text IS NULL
  AND (source_text ~ '<(bpt|ept|ph|it|x|g|hi|sub)\y' OR target_text ~ '<(bpt|ept|ph|it|x|g|hi|sub)\y');

-- Clean up rich_text rows where the strip produced an empty value (markup-only
-- segments). Set them back to NULL so the frontend's plain-text fallback
-- path renders cleanly. Rare in practice.
UPDATE translation_memory
SET source_rich_text = NULL
WHERE source_rich_text IS NOT NULL AND source_rich_text = source_text;
UPDATE translation_memory
SET target_rich_text = NULL
WHERE target_rich_text IS NOT NULL AND target_rich_text = target_text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- This migration is destructive (changes plain-text content + drops duplicates).
-- The Down can't reconstruct the dropped rows or the original markup positions
-- precisely — but it CAN move rich_text back into source_text/target_text for
-- rows that were touched. Best-effort restore for rollback during a deploy.
UPDATE translation_memory
SET source_text = source_rich_text,
    source_hash = md5(source_rich_text)
WHERE source_rich_text IS NOT NULL;
UPDATE translation_memory
SET target_text = target_rich_text,
    target_hash = md5(target_rich_text)
WHERE target_rich_text IS NOT NULL;
-- (Don't NULL the rich_text columns — leave them so the up-migration is a no-op
--  if re-run. Migration 020's Down handles dropping the columns entirely.)

-- +goose StatementEnd
