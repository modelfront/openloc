-- +goose Up
-- +goose StatementBegin

-- Split and join in the CAT editor change one job (file + target language)
-- only. A segment with target_language NULL is shared by every language of
-- its file; a segment with target_language set is a language row, made by a
-- split or join, and only ever owns a translation in that language. A job's
-- rows are the segments that own a translation in its language.
--
-- position stays the import number; sub_position orders the pieces of a split
-- inside one position (number "3.1" is position 2, sub_position 1).
-- export_key is the original key a language row rebuilds on export; pieces of
-- one key are joined with gap_before (the source between a piece and the one
-- before it) and their chip ids shifted back by chip_base.
ALTER TABLE segments
    ADD COLUMN target_language text,
    ADD COLUMN sub_position    integer NOT NULL DEFAULT 0,
    ADD COLUMN export_key      text,
    ADD COLUMN gap_before      text,
    ADD COLUMN chip_base       integer NOT NULL DEFAULT 0;

ALTER TABLE segments
    ADD CONSTRAINT segments_language_row_shape CHECK (
        (target_language IS NULL AND sub_position = 0 AND export_key IS NULL
            AND gap_before IS NULL AND chip_base = 0)
        OR (target_language IS NOT NULL AND export_key IS NOT NULL
            AND sub_position >= 0 AND chip_base >= 0)
    );

DROP INDEX IF EXISTS idx_segments_file_position;
CREATE INDEX idx_segments_file_position ON segments (file_id, position, sub_position);
CREATE INDEX idx_segments_language_rows ON segments (file_id, target_language)
    WHERE target_language IS NOT NULL;

-- A language row owns a translation in its own language only.
CREATE OR REPLACE FUNCTION translations_language_row_check() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM new_rows n
        JOIN segments s ON s.id = n.segment_id
        WHERE s.target_language IS NOT NULL AND s.target_language <> n.target_language
    ) THEN
        RAISE EXCEPTION 'a language segment owns translations in its own language only'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER translations_language_row_ins AFTER INSERT ON translations
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION translations_language_row_check();
CREATE TRIGGER translations_language_row_upd AFTER UPDATE ON translations
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION translations_language_row_check();

-- A comment moved to another row by a join keeps the number of the row it
-- was written on.
ALTER TABLE segment_comments ADD COLUMN from_segment text;

-- A change to a language row recounts that job only; a shared row still
-- recounts every language of its file.
CREATE OR REPLACE FUNCTION job_translation_stats_on_segments() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    files bigint[];
    langs text[];
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- A deleted project's files are left out: nothing reads their counts,
        -- and its purge deletes them with the files instead of recounting a
        -- file after every batch of its segments.
        SELECT array_agg(o.file_id), array_agg(o.lang) INTO files, langs
        FROM (SELECT DISTINCT file_id, target_language AS lang FROM old_rows) o
        WHERE NOT EXISTS (
            SELECT 1 FROM files f JOIN projects p ON p.id = f.project_id
            WHERE f.id = o.file_id AND p.deleted_at IS NOT NULL
        );
    ELSE
        SELECT array_agg(c.file_id), array_agg(c.lang) INTO files, langs FROM (
            SELECT DISTINCT file_id, lang FROM (
                SELECT n.file_id, n.target_language AS lang
                FROM new_rows n JOIN old_rows o ON o.id = n.id
                WHERE (n.word_count, n.file_id, n.target_language)
                      IS DISTINCT FROM (o.word_count, o.file_id, o.target_language)
                UNION ALL
                SELECT o.file_id, o.target_language
                FROM new_rows n JOIN old_rows o ON o.id = n.id
                WHERE (n.file_id, n.target_language) IS DISTINCT FROM (o.file_id, o.target_language)
            ) changed
        ) c;
    END IF;
    IF files IS NOT NULL THEN
        PERFORM job_translation_stats_correct(files, langs, false, true);
        PERFORM job_translation_stats_compact(files, langs);
    END IF;
    RETURN NULL;
END
$$;

-- +goose StatementEnd

-- Safe inside goose's transaction: nothing here uses the new values.
ALTER TYPE translation_history_kind ADD VALUE 'split';
ALTER TYPE translation_history_kind ADD VALUE 'joined';

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION job_translation_stats_on_segments() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    files bigint[];
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT array_agg(o.file_id) INTO files FROM (SELECT DISTINCT file_id FROM old_rows) o
        WHERE NOT EXISTS (
            SELECT 1 FROM files f JOIN projects p ON p.id = f.project_id
            WHERE f.id = o.file_id AND p.deleted_at IS NOT NULL
        );
    ELSE
        SELECT array_agg(DISTINCT file_id) INTO files FROM (
            SELECT n.file_id FROM new_rows n JOIN old_rows o ON o.id = n.id
            WHERE (n.word_count, n.file_id) IS DISTINCT FROM (o.word_count, o.file_id)
            UNION ALL
            SELECT o.file_id FROM new_rows n JOIN old_rows o ON o.id = n.id
            WHERE n.file_id IS DISTINCT FROM o.file_id
        ) f;
    END IF;
    IF files IS NOT NULL THEN
        PERFORM job_translation_stats_correct(files, array_fill(NULL::text, ARRAY[cardinality(files)]), false, true);
        PERFORM job_translation_stats_compact(files, array_fill(NULL::text, ARRAY[cardinality(files)]));
    END IF;
    RETURN NULL;
END
$$;

-- Lossy: the pieces of a split or join cannot be folded back into the shared
-- rows. Each job that has language rows gets an empty translation again on
-- every shared row it gave up, then the language rows and their translations
-- are dropped. Jobs that were never split or joined keep everything.
INSERT INTO translations (segment_id, target_language, status)
SELECT s.id, l.lang, 'needs_review'
FROM (SELECT DISTINCT file_id, target_language AS lang FROM segments
      WHERE target_language IS NOT NULL) l
JOIN segments s ON s.file_id = l.file_id AND s.target_language IS NULL
WHERE NOT EXISTS (
    SELECT 1 FROM translations t WHERE t.segment_id = s.id AND t.target_language = l.lang
);
DELETE FROM translation_history WHERE kind IN ('split', 'joined');
DELETE FROM translations t USING segments s
WHERE s.id = t.segment_id AND s.target_language IS NOT NULL;
DELETE FROM segments WHERE target_language IS NOT NULL;

CREATE TYPE translation_history_kind_old AS ENUM (
    'tm', 'mt', 'ape', 'human', 'propagation', 'file_import', 'cleared',
    'approved', 'auto_approved', 'unconfirmed'
);
ALTER TABLE translation_history
    ALTER COLUMN kind TYPE translation_history_kind_old
    USING kind::text::translation_history_kind_old;
DROP TYPE translation_history_kind;
ALTER TYPE translation_history_kind_old RENAME TO translation_history_kind;

ALTER TABLE segment_comments DROP COLUMN from_segment;

DROP TRIGGER IF EXISTS translations_language_row_upd ON translations;
DROP TRIGGER IF EXISTS translations_language_row_ins ON translations;
DROP FUNCTION IF EXISTS translations_language_row_check();

DROP INDEX IF EXISTS idx_segments_language_rows;
DROP INDEX IF EXISTS idx_segments_file_position;
CREATE INDEX idx_segments_file_position ON segments USING btree (file_id, "position");

ALTER TABLE segments DROP CONSTRAINT segments_language_row_shape;
ALTER TABLE segments
    DROP COLUMN chip_base,
    DROP COLUMN gap_before,
    DROP COLUMN export_key,
    DROP COLUMN sub_position,
    DROP COLUMN target_language;

-- +goose StatementEnd
