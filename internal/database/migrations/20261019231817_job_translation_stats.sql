-- +goose NO TRANSACTION
-- +goose Up
-- Segment and word counts per job (file × target language), kept up to date
-- by triggers so the job list, the progress report and the stuck-job list
-- read a few rows per job instead of every translation.
--
-- Each row holds raw facts for one combination of the columns that the
-- readers bucket on; the readers derive their buckets at read time. A job's
-- numbers are the SUM of its rows (MAX for last_activity_at). text_state: 0
-- no text (NULL), 1 empty text, 2 text. predict_this_round: an AI verdict
-- produced in the job's current step round (predict_outcome set, and the
-- round's translation_step_statuses row has no start_predict_outcome or does
-- not exist). last_activity_at: the newest translations.updated_at.
--
-- Locking:
-- - translation writes only INSERT delta rows, so writers to one job never
--   wait on each other for the counts;
-- - compaction and the activity reset change only rows no other transaction
--   holds (SKIP LOCKED), so they never wait either;
-- - a recount inserts the difference between a live count and the stored
--   rows after taking advisory locks for its jobs, so two recounts of one job
--   run one after the other and the second sees the first;
-- - a step change marks its jobs in job_translation_stats_pending; at commit
--   each marked job is recounted once, after locking its translations, so a
--   write that raced the step change is counted under the job's final step.
--
-- No transaction: each statement commits on its own, so the triggers lock
-- their tables only for the moment they are created. The backfill migration
-- recounts every job afterwards.
CREATE TABLE IF NOT EXISTS job_translation_stats (
    file_id            bigint NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    target_language    text NOT NULL,
    status             translation_status NOT NULL,
    text_state         smallint NOT NULL CHECK (text_state BETWEEN 0 AND 2),
    step_edited        boolean NOT NULL,
    edited             boolean NOT NULL,
    predict_this_round boolean NOT NULL,
    locked             boolean NOT NULL,
    segments           bigint NOT NULL,
    words              bigint NOT NULL,
    last_activity_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_job_translation_stats_job
    ON job_translation_stats (file_id, target_language);

-- Jobs whose step changed in an open transaction; emptied at its commit.
CREATE TABLE IF NOT EXISTS job_translation_stats_pending (
    file_id         bigint NOT NULL,
    target_language text NOT NULL,
    PRIMARY KEY (file_id, target_language)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION translation_text_state(t text) RETURNS smallint
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN t IS NULL THEN 0 WHEN t = '' THEN 1 ELSE 2 END::smallint
$$;
-- +goose StatementEnd

-- Folds a job's rows into one per combination once it has more than 32 rows
-- and at least two per combination (p_langs NULL for an entry means every
-- language of that file). Only rows no other transaction holds are folded.
-- Rows whose counts sum to zero are dropped, except the one carrying the
-- newest activity.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_compact(p_files bigint[], p_langs text[]) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    WITH k AS (
        SELECT DISTINCT st.file_id, st.target_language AS lang
        FROM unnest(p_files, p_langs) AS k(file_id, lang)
        JOIN job_translation_stats st
          ON st.file_id = k.file_id AND (k.lang IS NULL OR st.target_language = k.lang)
    ), due AS (
        SELECT k.file_id, k.lang
        FROM k
        CROSS JOIN LATERAL (
            SELECT count(*) AS n,
                   count(DISTINCT (st.status, st.text_state, st.step_edited, st.edited,
                                   st.predict_this_round, st.locked)) AS combinations
            FROM job_translation_stats st
            WHERE st.file_id = k.file_id AND st.target_language = k.lang
        ) c
        WHERE c.n > 32 AND c.n >= 2 * c.combinations
    ), free AS (
        SELECT st.ctid
        FROM job_translation_stats st
        JOIN due ON st.file_id = due.file_id AND st.target_language = due.lang
        FOR UPDATE OF st SKIP LOCKED
    ), gone AS (
        DELETE FROM job_translation_stats st
        WHERE st.ctid IN (SELECT ctid FROM free)
        RETURNING st.*
    ), folded AS (
        SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
               sum(segments) AS segments, sum(words) AS words, max(last_activity_at) AS last_activity_at,
               max(max(last_activity_at)) OVER (PARTITION BY file_id, target_language) AS job_last_activity_at
        FROM gone
        GROUP BY file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked
    )
    INSERT INTO job_translation_stats (file_id, target_language, status, text_state, step_edited, edited,
                                       predict_this_round, locked, segments, words, last_activity_at)
    SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
           segments, words, last_activity_at
    FROM folded
    WHERE segments <> 0 OR words <> 0 OR last_activity_at = job_last_activity_at;
END
$$;
-- +goose StatementEnd

-- Brings the given jobs in line with a live recount by inserting the
-- difference (p_langs NULL for an entry means every language of that file).
-- Reads only those jobs' rows.
-- p_lock_rows also locks the jobs' translations first, so writes still open
-- on them commit before the recount reads them and later ones wait for this
-- transaction. p_reset_activity first clears stored activity newer than the
-- live one, for writes that can lower it (a delete, an update that moves
-- updated_at back).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_correct(
    p_files bigint[], p_langs text[],
    p_lock_rows boolean DEFAULT false, p_reset_activity boolean DEFAULT false
) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    -- Lock order, the same on every path: a job's translation rows, then its
    -- advisory locks. The delete and update triggers already hold their rows
    -- when they get here. Rows are locked in id order, so two transactions
    -- locking one job's rows queue instead of deadlocking.
    IF p_lock_rows THEN
        PERFORM 1 FROM (
            SELECT t.id
            FROM unnest(p_files, p_langs) AS k(file_id, lang)
            JOIN segments s ON s.file_id = k.file_id
            JOIN translations t ON t.segment_id = s.id AND (k.lang IS NULL OR t.target_language = k.lang)
            ORDER BY t.id
            FOR NO KEY UPDATE OF t
        ) locked;
    END IF;

    -- Advisory locks, each in its own statement, so the recount below reads
    -- after any earlier recount of the same job has committed. A whole-file
    -- recount (lang NULL) takes the file lock exclusive; a job recount takes
    -- it shared plus an exclusive lock on its (file, language), so recounts
    -- of different languages of one file do not wait for each other.
    PERFORM CASE WHEN whole
                 THEN pg_advisory_xact_lock(1246057300, hashtext(f::text))
                 ELSE pg_advisory_xact_lock_shared(1246057300, hashtext(f::text)) END
    FROM (SELECT k.f, bool_or(k.lang IS NULL) AS whole
          FROM unnest(p_files, p_langs) AS k(f, lang)
          GROUP BY k.f ORDER BY k.f) files;
    PERFORM pg_advisory_xact_lock(1246057301, hashtext(k.f::text || '|' || k.lang))
    FROM (SELECT DISTINCT k.f, k.lang
          FROM unnest(p_files, p_langs) AS k(f, lang)
          WHERE k.lang IS NOT NULL
          ORDER BY k.f, k.lang) k;

    IF p_reset_activity THEN
        WITH k AS (
            SELECT DISTINCT k.file_id, k.lang
            FROM unnest(p_files, p_langs) AS k(file_id, lang)
        ), live AS (
            SELECT s.file_id, t.target_language, max(t.updated_at) AS last_activity_at
            FROM k
            JOIN segments s ON s.file_id = k.file_id
            JOIN translations t ON t.segment_id = s.id AND (k.lang IS NULL OR t.target_language = k.lang)
            GROUP BY 1, 2
        ), newer AS (
            SELECT st.ctid
            FROM job_translation_stats st
            JOIN k ON st.file_id = k.file_id AND (k.lang IS NULL OR st.target_language = k.lang)
            LEFT JOIN live ON live.file_id = st.file_id AND live.target_language = st.target_language
            WHERE st.last_activity_at > COALESCE(live.last_activity_at, '-infinity')
            FOR UPDATE OF st SKIP LOCKED
        )
        UPDATE job_translation_stats st
        SET last_activity_at = NULL
        WHERE st.ctid IN (SELECT ctid FROM newer);
    END IF;

    WITH k AS (
        SELECT DISTINCT k.file_id, k.lang
        FROM unnest(p_files, p_langs) AS k(file_id, lang)
    ), live AS (
        SELECT s.file_id, t.target_language, t.status,
               translation_text_state(t.active_text) AS text_state,
               t.step_edited, t.edited_by IS NOT NULL AS edited,
               (t.predict_outcome IS NOT NULL AND cur.start_predict_outcome IS NULL) AS predict_this_round,
               t.locked,
               count(*) AS segments, sum(s.word_count) AS words, max(t.updated_at) AS last_activity_at
        FROM k
        JOIN segments s ON s.file_id = k.file_id
        JOIN translations t ON t.segment_id = s.id AND (k.lang IS NULL OR t.target_language = k.lang)
        LEFT JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
        LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
        LEFT JOIN translation_step_statuses cur
          ON cur.translation_id = t.id AND cur.job_step_assignment_id = jsa.id AND cur.round = jsa.round
        GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
    ), have AS (
        SELECT st.file_id, st.target_language, st.status, st.text_state, st.step_edited, st.edited,
               st.predict_this_round, st.locked,
               sum(st.segments) AS segments, sum(st.words) AS words, max(st.last_activity_at) AS last_activity_at
        FROM k
        JOIN job_translation_stats st
          ON st.file_id = k.file_id AND (k.lang IS NULL OR st.target_language = k.lang)
        GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
    ), diff AS (
        SELECT COALESCE(l.file_id, h.file_id) AS file_id,
               COALESCE(l.target_language, h.target_language) AS target_language,
               COALESCE(l.status, h.status) AS status,
               COALESCE(l.text_state, h.text_state) AS text_state,
               COALESCE(l.step_edited, h.step_edited) AS step_edited,
               COALESCE(l.edited, h.edited) AS edited,
               COALESCE(l.predict_this_round, h.predict_this_round) AS predict_this_round,
               COALESCE(l.locked, h.locked) AS locked,
               COALESCE(l.segments, 0) - COALESCE(h.segments, 0) AS segments,
               COALESCE(l.words, 0) - COALESCE(h.words, 0) AS words,
               CASE WHEN h.last_activity_at IS NULL OR l.last_activity_at > h.last_activity_at
                    THEN l.last_activity_at END AS last_activity_at
        FROM live l
        FULL JOIN have h
          ON  h.file_id = l.file_id AND h.target_language = l.target_language
          AND h.status = l.status AND h.text_state = l.text_state
          AND h.step_edited = l.step_edited AND h.edited = l.edited
          AND h.predict_this_round = l.predict_this_round AND h.locked = l.locked
    )
    INSERT INTO job_translation_stats (file_id, target_language, status, text_state, step_edited, edited,
                                       predict_this_round, locked, segments, words, last_activity_at)
    SELECT d.file_id, d.target_language, d.status, d.text_state, d.step_edited, d.edited,
           d.predict_this_round, d.locked, d.segments, d.words, d.last_activity_at
    FROM diff d
    WHERE (d.segments <> 0 OR d.words <> 0 OR d.last_activity_at IS NOT NULL)
      AND EXISTS (SELECT 1 FROM files f WHERE f.id = d.file_id);
END
$$;
-- +goose StatementEnd

-- Recounts every job, one transaction per batch of files: at most
-- p_max_files files and at most p_max_segments translations, or one file
-- (a file weighs its segments times its jobs). A file heavier than that on
-- its own is recounted one language per transaction, so a step
-- change on one of its languages waits for that language only. The backfill
-- and the repair path: CALL job_translation_stats_correct_all(); outside a
-- transaction block. Safe to run while the app writes.
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE job_translation_stats_correct_all(
    p_max_files int DEFAULT 100, p_max_segments bigint DEFAULT 50000
)
LANGUAGE plpgsql AS $$
DECLARE
    last_id bigint := 0;
    ids bigint[];
    first_weight bigint;
    lang text;
BEGIN
    LOOP
        SELECT array_agg(id ORDER BY id), (array_agg(weight ORDER BY id))[1] INTO ids, first_weight
        FROM (
            SELECT id, weight, sum(weight) OVER (ORDER BY id) AS running,
                   row_number() OVER (ORDER BY id) AS n
            FROM (
                SELECT f.id, f.segment_count::bigint
                       * GREATEST(1, (SELECT count(*) FROM jobs j WHERE j.file_id = f.id)) AS weight
                FROM files f WHERE f.id > last_id ORDER BY f.id LIMIT p_max_files
            ) f
        ) b
        -- A batch ends before the file that would take it past the limit,
        -- so a heavy file always starts a batch of its own.
        WHERE n = 1 OR running <= p_max_segments;
        EXIT WHEN ids IS NULL;
        IF cardinality(ids) = 1 AND first_weight > p_max_segments THEN
            -- Every language the file has translations or counts in.
            FOR lang IN
                SELECT t.target_language FROM segments s JOIN translations t ON t.segment_id = s.id
                WHERE s.file_id = ids[1]
                UNION
                SELECT st.target_language FROM job_translation_stats st WHERE st.file_id = ids[1]
                ORDER BY 1
            LOOP
                PERFORM job_translation_stats_correct(ids, ARRAY[lang]);
                PERFORM job_translation_stats_compact(ids, ARRAY[lang]);
                COMMIT;
            END LOOP;
        ELSE
            PERFORM job_translation_stats_correct(ids, array_fill(NULL::text, ARRAY[cardinality(ids)]));
            PERFORM job_translation_stats_compact(ids, array_fill(NULL::text, ARRAY[cardinality(ids)]));
            COMMIT;
        END IF;
        last_id := ids[cardinality(ids)];
    END LOOP;
END
$$;
-- +goose StatementEnd

-- translations INSERT / UPDATE: one delta row per changed combination,
-- read straight from the transition tables. An update groups old (-1) and
-- new (+1) versions by what they are counted on, so versions it did not
-- change cancel; updated_at is in the grouping, so an update that only moved
-- it leaves a pair whose counts cancel and whose +1 side records the
-- activity. Grouping instead of joining old_rows to new_rows: the planner has
-- no statistics on transition tables. The INSERT and UPDATE branches differ
-- only in where v comes from: a statement may name only the transition
-- tables its trigger has, and static statements keep their cached plans.
-- DELETE: a recount of the affected jobs; rows whose segment went in the
-- same statement are left to the segments trigger. A delete, or an update
-- that moved updated_at back, also resets the jobs' activity.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_on_translations() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    files bigint[];
    langs text[];
BEGIN
    IF TG_OP = 'INSERT' THEN
        WITH v AS (
            SELECT id, segment_id, target_language, status, translation_text_state(active_text) AS text_state,
                   step_edited, edited_by IS NOT NULL AS edited, predict_outcome IS NOT NULL AS has_predict,
                   locked, updated_at, 1 AS sign
            FROM new_rows
        ),
    facts AS (
        SELECT s.file_id, v.target_language, v.status, v.text_state, v.step_edited, v.edited,
               (v.has_predict AND cur.start_predict_outcome IS NULL) AS predict_this_round,
               v.locked, v.sign, s.word_count, v.updated_at
        FROM v
        JOIN segments s ON s.id = v.segment_id
        LEFT JOIN jobs j ON j.file_id = s.file_id AND j.target_language = v.target_language
        LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
        LEFT JOIN translation_step_statuses cur
          ON cur.translation_id = v.id AND cur.job_step_assignment_id = jsa.id AND cur.round = jsa.round
    ), delta AS (
        SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
               sum(sign) AS segments, sum(sign * word_count) AS words,
               max(updated_at) FILTER (WHERE sign > 0) AS last_activity_at
        FROM facts
        GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
    ), ins AS (
        INSERT INTO job_translation_stats (file_id, target_language, status, text_state, step_edited, edited,
                                           predict_this_round, locked, segments, words, last_activity_at)
        SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
               segments, words, last_activity_at
        FROM delta
        WHERE segments <> 0 OR words <> 0 OR last_activity_at IS NOT NULL
        RETURNING file_id, target_language
    )
    SELECT array_agg(file_id), array_agg(target_language) INTO files, langs
    FROM (SELECT DISTINCT file_id, target_language FROM ins) k;
        IF files IS NOT NULL THEN
            PERFORM job_translation_stats_compact(files, langs);
        END IF;
    ELSIF TG_OP = 'UPDATE' THEN
        WITH v AS (
            SELECT id, segment_id, target_language, status, text_state, step_edited, edited, has_predict,
                   locked, updated_at, sum(sign) AS sign
            FROM (
                SELECT 1 AS sign, id, segment_id, target_language, status, translation_text_state(active_text) AS text_state,
                       step_edited, edited_by IS NOT NULL AS edited, predict_outcome IS NOT NULL AS has_predict,
                       locked, updated_at
                FROM new_rows
                UNION ALL
                SELECT -1, id, segment_id, target_language, status, translation_text_state(active_text),
                       step_edited, edited_by IS NOT NULL, predict_outcome IS NOT NULL, locked, updated_at
                FROM old_rows
            ) versions
            GROUP BY id, segment_id, target_language, status, text_state, step_edited, edited, has_predict, locked, updated_at
            HAVING sum(sign) <> 0
        ),
    facts AS (
        SELECT s.file_id, v.target_language, v.status, v.text_state, v.step_edited, v.edited,
               (v.has_predict AND cur.start_predict_outcome IS NULL) AS predict_this_round,
               v.locked, v.sign, s.word_count, v.updated_at
        FROM v
        JOIN segments s ON s.id = v.segment_id
        LEFT JOIN jobs j ON j.file_id = s.file_id AND j.target_language = v.target_language
        LEFT JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
        LEFT JOIN translation_step_statuses cur
          ON cur.translation_id = v.id AND cur.job_step_assignment_id = jsa.id AND cur.round = jsa.round
    ), delta AS (
        SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
               sum(sign) AS segments, sum(sign * word_count) AS words,
               max(updated_at) FILTER (WHERE sign > 0) AS last_activity_at
        FROM facts
        GROUP BY 1, 2, 3, 4, 5, 6, 7, 8
    ), ins AS (
        INSERT INTO job_translation_stats (file_id, target_language, status, text_state, step_edited, edited,
                                           predict_this_round, locked, segments, words, last_activity_at)
        SELECT file_id, target_language, status, text_state, step_edited, edited, predict_this_round, locked,
               segments, words, last_activity_at
        FROM delta
        WHERE segments <> 0 OR words <> 0 OR last_activity_at IS NOT NULL
        RETURNING file_id, target_language
    )
    SELECT array_agg(file_id), array_agg(target_language) INTO files, langs
    FROM (SELECT DISTINCT file_id, target_language FROM ins) k;
        IF files IS NOT NULL THEN
            PERFORM job_translation_stats_compact(files, langs);
        END IF;
        -- An update that moved updated_at back can lower the job's activity.
        SELECT array_agg(file_id), array_agg(target_language) INTO files, langs
        FROM (
            SELECT DISTINCT s.file_id, b.target_language
            FROM (
                SELECT max(segment_id) AS segment_id, max(target_language) AS target_language
                FROM (SELECT 1 AS sign, id, segment_id, target_language, updated_at FROM new_rows
                      UNION ALL
                      SELECT -1, id, segment_id, target_language, updated_at FROM old_rows) v
                GROUP BY id
                HAVING max(updated_at) FILTER (WHERE sign > 0) < max(updated_at) FILTER (WHERE sign < 0)
            ) b
            JOIN segments s ON s.id = b.segment_id
        ) k;
        IF files IS NOT NULL THEN
            PERFORM job_translation_stats_correct(files, langs, false, true);
        END IF;
    ELSE
        SELECT array_agg(file_id), array_agg(target_language) INTO files, langs
        FROM (SELECT DISTINCT s.file_id, o.target_language
              FROM old_rows o JOIN segments s ON s.id = o.segment_id) k;
        IF files IS NOT NULL THEN
            PERFORM job_translation_stats_correct(files, langs, false, true);
            PERFORM job_translation_stats_compact(files, langs);
        END IF;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- segments DELETE, or a change of word_count / file: recount those files and
-- reset their activity.
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

-- A change to which step round is current moves predict_this_round for the
-- whole job: mark the jobs whose current step, round, or current round start
-- rows changed. A job is marked once per transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_on_step_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'jobs' THEN
        IF TG_OP = 'INSERT' THEN
            INSERT INTO job_translation_stats_pending (file_id, target_language)
            SELECT DISTINCT file_id, target_language FROM new_rows WHERE current_step_id IS NOT NULL
            ORDER BY 1, 2
            ON CONFLICT DO NOTHING;
        ELSIF TG_OP = 'DELETE' THEN
            INSERT INTO job_translation_stats_pending (file_id, target_language)
            SELECT DISTINCT file_id, target_language FROM old_rows WHERE current_step_id IS NOT NULL
            ORDER BY 1, 2
            ON CONFLICT DO NOTHING;
        ELSE
            INSERT INTO job_translation_stats_pending (file_id, target_language)
            SELECT DISTINCT n.file_id, n.target_language
            FROM new_rows n JOIN old_rows o ON o.id = n.id
            WHERE n.current_step_id IS DISTINCT FROM o.current_step_id
            ORDER BY 1, 2
            ON CONFLICT DO NOTHING;
        END IF;
    ELSIF TG_TABLE_NAME = 'job_step_assignments' THEN
        INSERT INTO job_translation_stats_pending (file_id, target_language)
        SELECT DISTINCT j.file_id, j.target_language
        FROM new_rows n
        JOIN old_rows o ON o.id = n.id
        JOIN jobs j ON j.current_step_id = n.id
        WHERE n.round IS DISTINCT FROM o.round
        ORDER BY 1, 2
        ON CONFLICT DO NOTHING;
    ELSIF TG_OP = 'INSERT' THEN -- translation_step_statuses: only a job's current round matters.
        INSERT INTO job_translation_stats_pending (file_id, target_language)
        SELECT DISTINCT j.file_id, j.target_language
        FROM (SELECT DISTINCT job_step_assignment_id, round FROM new_rows) r
        JOIN job_step_assignments jsa ON jsa.id = r.job_step_assignment_id AND jsa.round = r.round
        JOIN jobs j ON j.current_step_id = jsa.id
        ORDER BY 1, 2
        ON CONFLICT DO NOTHING;
    ELSIF TG_OP = 'DELETE' THEN
        INSERT INTO job_translation_stats_pending (file_id, target_language)
        SELECT DISTINCT j.file_id, j.target_language
        FROM (SELECT DISTINCT job_step_assignment_id, round FROM old_rows) r
        JOIN job_step_assignments jsa ON jsa.id = r.job_step_assignment_id AND jsa.round = r.round
        JOIN jobs j ON j.current_step_id = jsa.id
        ORDER BY 1, 2
        ON CONFLICT DO NOTHING;
    ELSE
        INSERT INTO job_translation_stats_pending (file_id, target_language)
        SELECT DISTINCT j.file_id, j.target_language
        FROM (SELECT DISTINCT n.job_step_assignment_id, n.round
              FROM new_rows n JOIN old_rows o ON o.id = n.id
              WHERE n.start_predict_outcome IS DISTINCT FROM o.start_predict_outcome) r
        JOIN job_step_assignments jsa ON jsa.id = r.job_step_assignment_id AND jsa.round = r.round
        JOIN jobs j ON j.current_step_id = jsa.id
        ORDER BY 1, 2
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- At commit: recount a marked job after locking its translations, then
-- unmark it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION job_translation_stats_on_pending() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM job_translation_stats_correct(ARRAY[NEW.file_id], ARRAY[NEW.target_language], true, false);
    PERFORM job_translation_stats_compact(ARRAY[NEW.file_id], ARRAY[NEW.target_language]);
    DELETE FROM job_translation_stats_pending
    WHERE file_id = NEW.file_id AND target_language = NEW.target_language;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS job_translation_stats_recount ON job_translation_stats_pending;
CREATE CONSTRAINT TRIGGER job_translation_stats_recount AFTER INSERT ON job_translation_stats_pending
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION job_translation_stats_on_pending();

DROP TRIGGER IF EXISTS job_translation_stats_ins ON translations;
CREATE TRIGGER job_translation_stats_ins AFTER INSERT ON translations
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_translations();

DROP TRIGGER IF EXISTS job_translation_stats_upd ON translations;
CREATE TRIGGER job_translation_stats_upd AFTER UPDATE ON translations
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_translations();

DROP TRIGGER IF EXISTS job_translation_stats_del ON translations;
CREATE TRIGGER job_translation_stats_del AFTER DELETE ON translations
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_translations();

DROP TRIGGER IF EXISTS job_translation_stats_del ON segments;
CREATE TRIGGER job_translation_stats_del AFTER DELETE ON segments
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_segments();

DROP TRIGGER IF EXISTS job_translation_stats_upd ON segments;
CREATE TRIGGER job_translation_stats_upd AFTER UPDATE ON segments
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_segments();

DROP TRIGGER IF EXISTS job_translation_stats_ins ON jobs;
CREATE TRIGGER job_translation_stats_ins AFTER INSERT ON jobs
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_upd ON jobs;
CREATE TRIGGER job_translation_stats_upd AFTER UPDATE ON jobs
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_del ON jobs;
CREATE TRIGGER job_translation_stats_del AFTER DELETE ON jobs
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_upd ON job_step_assignments;
CREATE TRIGGER job_translation_stats_upd AFTER UPDATE ON job_step_assignments
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_ins ON translation_step_statuses;
CREATE TRIGGER job_translation_stats_ins AFTER INSERT ON translation_step_statuses
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_upd ON translation_step_statuses;
CREATE TRIGGER job_translation_stats_upd AFTER UPDATE ON translation_step_statuses
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

DROP TRIGGER IF EXISTS job_translation_stats_del ON translation_step_statuses;
CREATE TRIGGER job_translation_stats_del AFTER DELETE ON translation_step_statuses
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION job_translation_stats_on_step_change();

-- +goose Down
DROP TRIGGER IF EXISTS job_translation_stats_del ON translation_step_statuses;
DROP TRIGGER IF EXISTS job_translation_stats_upd ON translation_step_statuses;
DROP TRIGGER IF EXISTS job_translation_stats_ins ON translation_step_statuses;
DROP TRIGGER IF EXISTS job_translation_stats_upd ON job_step_assignments;
DROP TRIGGER IF EXISTS job_translation_stats_del ON jobs;
DROP TRIGGER IF EXISTS job_translation_stats_upd ON jobs;
DROP TRIGGER IF EXISTS job_translation_stats_ins ON jobs;
DROP TRIGGER IF EXISTS job_translation_stats_upd ON segments;
DROP TRIGGER IF EXISTS job_translation_stats_del ON segments;
DROP TRIGGER IF EXISTS job_translation_stats_del ON translations;
DROP TRIGGER IF EXISTS job_translation_stats_upd ON translations;
DROP TRIGGER IF EXISTS job_translation_stats_ins ON translations;
DROP TRIGGER IF EXISTS job_translation_stats_recount ON job_translation_stats_pending;
DROP FUNCTION IF EXISTS job_translation_stats_on_pending();
DROP FUNCTION IF EXISTS job_translation_stats_on_step_change();
DROP FUNCTION IF EXISTS job_translation_stats_on_segments();
DROP FUNCTION IF EXISTS job_translation_stats_on_translations();
DROP PROCEDURE IF EXISTS job_translation_stats_correct_all(int, bigint);
DROP FUNCTION IF EXISTS job_translation_stats_correct(bigint[], text[], boolean, boolean);
DROP FUNCTION IF EXISTS job_translation_stats_compact(bigint[], text[]);
DROP FUNCTION IF EXISTS translation_text_state(text);
DROP TABLE IF EXISTS job_translation_stats_pending;
DROP TABLE IF EXISTS job_translation_stats;
