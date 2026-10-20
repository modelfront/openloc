-- +goose Up
-- A segment delete skips recounting the files of a deleted project, so a
-- project purge removes its segments in batches without a recount of what is
-- left of each file after every batch.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_on_segments() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    files bigint[];
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- A deleted project's files are left out: nothing reads their counts,
        -- and its purge deletes them with the files instead of recounting a
        -- file after every batch of its segments.
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_on_segments() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    files bigint[];
BEGIN
    IF TG_OP = 'DELETE' THEN
        SELECT array_agg(DISTINCT file_id) INTO files FROM old_rows;
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
-- +goose StatementEnd
