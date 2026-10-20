-- +goose Up
-- Saved analyses + per-(job, target_language) breakdowns.
--
-- An "analysis" is an immutable snapshot of project leverage: a PM
-- picks one or more jobs, clicks Run, and a worker computes a per-job × target-
-- language breakdown across 6 bands (tm_101, tm_100, tm_85_99, tm_65_84, new,
-- repetitions) with totals + auto_approved subsets. Snapshots are quotable and
-- survive later job/TM deletions.
--
-- Touches:
--   - new enum: analysis_status ('pending' | 'running' | 'completed' | 'failed')
--   - new table: analyses (the entity, with job_ids bigint[] snapshot)
--   - new table: job_analysis (one row per (analysis, job, target_language),
--     48 metric columns = 6 bands × 8 = (segments / words / characters /
--     weighted_words) × (totals / auto_approved subset))
--   - GIN index on analyses.job_ids for reverse lookups ("which analyses
--     include job X")
--
-- Design references in the planning doc: §"Data model", §"Numbers source",
-- §"Pretranslate-completion gate", §"Auto-approved semantics".
--
-- Reserved-but-empty bands this round: tm_101 (ICE detection not added to
-- pretranslate yet) and repetitions (behavior intentionally deferred). The
-- columns exist so the future features can start writing without a migration.

CREATE TYPE analysis_status AS ENUM (
    'pending',     -- created, worker hasn't picked it up
    'running',     -- worker started
    'completed',   -- breakdown rows inserted, immutable from here
    'failed'       -- worker hit a fatal error; error_message populated
);

CREATE TABLE analyses (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id        bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              text NOT NULL,
    status            analysis_status NOT NULL DEFAULT 'pending',
    error_message     text,
    source_language   text NOT NULL,
    -- Snapshot record of which jobs the PM selected at run time. No FK per
    -- element (Postgres arrays don't support it) — application-layer
    -- validation at POST time enforces "every id belongs to this project
    -- in this tenant." Stale IDs are accepted after jobs are deleted; the
    -- array stays as the historical record of what was selected.
    job_ids           bigint[] NOT NULL DEFAULT '{}',
    created_by        bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    completed_at      timestamptz,
    -- Weights + cost rates frozen at run time so re-rendering a year-old
    -- analysis shows the same dollar amount even if tenant rates moved.
    -- Same immutability pattern as cost-rate overrides.
    settings_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,

    CONSTRAINT analyses_name_nonempty CHECK (length(trim(name)) > 0),
    CONSTRAINT analyses_failed_has_message CHECK (
        status <> 'failed' OR (error_message IS NOT NULL AND length(error_message) > 0)
    ),
    CONSTRAINT analyses_completed_has_timestamp CHECK (
        (status IN ('completed', 'failed')) = (completed_at IS NOT NULL)
    )
);

CREATE INDEX analyses_project_idx     ON analyses (project_id, created_at DESC);
CREATE INDEX analyses_tenant_idx      ON analyses (tenant_id, created_at DESC);
CREATE INDEX analyses_status_idx      ON analyses (status) WHERE status IN ('pending', 'running');
-- GIN supports `WHERE job_ids @> ARRAY[X]` for "list analyses that include job X."
CREATE INDEX analyses_job_ids_gin_idx ON analyses USING GIN (job_ids);


-- job_analysis: one row per (analysis, job, target_language). Surrogate `id`
-- PK + UNIQUE constraint on the natural key, because `job_id` is nullable
-- (ON DELETE SET NULL — see planning doc "Snapshot survival on job deletion")
-- and Postgres can't use a nullable column in a primary key.
CREATE TABLE job_analysis (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    analysis_id        bigint NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    -- ON DELETE SET NULL preserves the breakdown row when the job is later
    -- deleted; job_name_snapshot keeps the UI rendering meaningful. job_ids[]
    -- on analyses retains the stale id as a record of selection.
    job_id             bigint REFERENCES jobs(id) ON DELETE SET NULL,
    target_language    text NOT NULL,
    -- Denormalized at write time so the UI shows "Marketing copy.docx" even
    -- after the job is deleted (then renders it with a "(deleted)" badge).
    job_name_snapshot  text NOT NULL,

    -- ── tm_101 band (ICE — reserved, written by future ICE-in-pretranslate work)
    tm_101_segments                      int NOT NULL DEFAULT 0,
    tm_101_words                         int NOT NULL DEFAULT 0,
    tm_101_characters                    int NOT NULL DEFAULT 0,
    tm_101_weighted_words                int NOT NULL DEFAULT 0,
    tm_101_segments_auto_approved        int NOT NULL DEFAULT 0,
    tm_101_words_auto_approved           int NOT NULL DEFAULT 0,
    tm_101_characters_auto_approved      int NOT NULL DEFAULT 0,
    tm_101_weighted_words_auto_approved  int NOT NULL DEFAULT 0,

    -- ── tm_100 band (exact)
    tm_100_segments                      int NOT NULL DEFAULT 0,
    tm_100_words                         int NOT NULL DEFAULT 0,
    tm_100_characters                    int NOT NULL DEFAULT 0,
    tm_100_weighted_words                int NOT NULL DEFAULT 0,
    tm_100_segments_auto_approved        int NOT NULL DEFAULT 0,
    tm_100_words_auto_approved           int NOT NULL DEFAULT 0,
    tm_100_characters_auto_approved      int NOT NULL DEFAULT 0,
    tm_100_weighted_words_auto_approved  int NOT NULL DEFAULT 0,

    -- ── tm_85_99 band (high fuzzy)
    tm_85_99_segments                    int NOT NULL DEFAULT 0,
    tm_85_99_words                       int NOT NULL DEFAULT 0,
    tm_85_99_characters                  int NOT NULL DEFAULT 0,
    tm_85_99_weighted_words              int NOT NULL DEFAULT 0,
    tm_85_99_segments_auto_approved      int NOT NULL DEFAULT 0,
    tm_85_99_words_auto_approved         int NOT NULL DEFAULT 0,
    tm_85_99_characters_auto_approved    int NOT NULL DEFAULT 0,
    tm_85_99_weighted_words_auto_approved int NOT NULL DEFAULT 0,

    -- ── tm_65_84 band (low fuzzy)
    tm_65_84_segments                    int NOT NULL DEFAULT 0,
    tm_65_84_words                       int NOT NULL DEFAULT 0,
    tm_65_84_characters                  int NOT NULL DEFAULT 0,
    tm_65_84_weighted_words              int NOT NULL DEFAULT 0,
    tm_65_84_segments_auto_approved      int NOT NULL DEFAULT 0,
    tm_65_84_words_auto_approved         int NOT NULL DEFAULT 0,
    tm_65_84_characters_auto_approved    int NOT NULL DEFAULT 0,
    tm_65_84_weighted_words_auto_approved int NOT NULL DEFAULT 0,

    -- ── new band (below 65% or no TM match)
    new_segments                         int NOT NULL DEFAULT 0,
    new_words                            int NOT NULL DEFAULT 0,
    new_characters                       int NOT NULL DEFAULT 0,
    new_weighted_words                   int NOT NULL DEFAULT 0,
    new_segments_auto_approved           int NOT NULL DEFAULT 0,
    new_words_auto_approved              int NOT NULL DEFAULT 0,
    new_characters_auto_approved         int NOT NULL DEFAULT 0,
    new_weighted_words_auto_approved     int NOT NULL DEFAULT 0,

    -- ── repetitions band (reserved — behavior deferred, always 0 this round)
    repetitions_segments                 int NOT NULL DEFAULT 0,
    repetitions_words                    int NOT NULL DEFAULT 0,
    repetitions_characters               int NOT NULL DEFAULT 0,
    repetitions_weighted_words           int NOT NULL DEFAULT 0,
    repetitions_segments_auto_approved   int NOT NULL DEFAULT 0,
    repetitions_words_auto_approved      int NOT NULL DEFAULT 0,
    repetitions_characters_auto_approved int NOT NULL DEFAULT 0,
    repetitions_weighted_words_auto_approved int NOT NULL DEFAULT 0,

    CONSTRAINT job_analysis_natural_key UNIQUE (analysis_id, job_id, target_language),
    -- Auto-approved is a subset, never an overlay. Enforced per band per metric.
    CONSTRAINT tm_101_aa_subset       CHECK (tm_101_segments_auto_approved      <= tm_101_segments      AND tm_101_words_auto_approved      <= tm_101_words      AND tm_101_characters_auto_approved      <= tm_101_characters      AND tm_101_weighted_words_auto_approved      <= tm_101_weighted_words),
    CONSTRAINT tm_100_aa_subset       CHECK (tm_100_segments_auto_approved      <= tm_100_segments      AND tm_100_words_auto_approved      <= tm_100_words      AND tm_100_characters_auto_approved      <= tm_100_characters      AND tm_100_weighted_words_auto_approved      <= tm_100_weighted_words),
    CONSTRAINT tm_85_99_aa_subset     CHECK (tm_85_99_segments_auto_approved    <= tm_85_99_segments    AND tm_85_99_words_auto_approved    <= tm_85_99_words    AND tm_85_99_characters_auto_approved    <= tm_85_99_characters    AND tm_85_99_weighted_words_auto_approved    <= tm_85_99_weighted_words),
    CONSTRAINT tm_65_84_aa_subset     CHECK (tm_65_84_segments_auto_approved    <= tm_65_84_segments    AND tm_65_84_words_auto_approved    <= tm_65_84_words    AND tm_65_84_characters_auto_approved    <= tm_65_84_characters    AND tm_65_84_weighted_words_auto_approved    <= tm_65_84_weighted_words),
    CONSTRAINT new_aa_subset          CHECK (new_segments_auto_approved         <= new_segments         AND new_words_auto_approved         <= new_words         AND new_characters_auto_approved         <= new_characters         AND new_weighted_words_auto_approved         <= new_weighted_words),
    CONSTRAINT repetitions_aa_subset  CHECK (repetitions_segments_auto_approved <= repetitions_segments AND repetitions_words_auto_approved <= repetitions_words AND repetitions_characters_auto_approved <= repetitions_characters AND repetitions_weighted_words_auto_approved <= repetitions_weighted_words)
);

CREATE INDEX job_analysis_analysis_idx ON job_analysis (analysis_id);
-- Job-scoped reverse lookup ("how does this job analyze across saved snapshots") —
-- partial because most rows have a job_id; deleted-job rows fall out naturally.
CREATE INDEX job_analysis_job_idx      ON job_analysis (job_id) WHERE job_id IS NOT NULL;


-- +goose Down
DROP TABLE IF EXISTS job_analysis;
DROP TABLE IF EXISTS analyses;
DROP TYPE  IF EXISTS analysis_status;
