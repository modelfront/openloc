-- +goose Up
-- Per-job stage timings. One row per job, forever.
--
-- # Why aggregates rather than an event row per occurrence
--
-- The expensive stages are loops: MT translates 50 segments a call and
-- ModelFront /predict scores 30 rows a batch, so a 1000-segment job runs the
-- engine ~20 times and the predictor ~34 times — before TM lookups, ingest or
-- retries. A row-per-occurrence table would grow by hundreds of rows per job
-- and need a GROUP BY before it could answer anything. Instead the recorder
-- (internal/timing) aggregates in memory for the duration of one task and
-- flushes once, so a job's entire history — upload pipeline, QE+APE step, every
-- later "Apply MT" — lands in a single row that accumulates. Per-occurrence
-- detail still goes to structured logs; this table is the aggregate a UI can
-- render without a GROUP BY.
--
-- # Self time vs total time
--
-- Spans nest: `mt.tag_transfer` runs inside `mt.db_save`, and every `task.*`
-- span wraps the whole pipeline. One duration per stage would therefore report
-- the same milliseconds under several names — MT's real 56% share of the work
-- displayed as 36% while the task span containing it was charted as its peer.
-- So each stage records two figures:
--
--   total_ms — inclusive: the whole span, children and all.
--   self_ms  — exclusive: total minus what its direct children accounted for.
--
-- ONLY self_ms may be summed. Summing it equals the wall clock exactly once at
-- any nesting depth, so double-counting is arithmetically impossible rather
-- than forbidden by convention — which matters because the slug vocabulary
-- (internal/timing/stages.go) is open and deliberately not enumerated here. A
-- task span's self_ms falls out as the uninstrumented overhead, no special case
-- needed.
--
-- `stages` maps a stage slug to its aggregate:
--   {"mt.engine_call": {"n": 20, "self_ms": 41230, "total_ms": 41230,
--                       "max_ms": 3100, "err": 1, "meta": {"engine": "deepl"}}}
CREATE TABLE job_timings (
    job_id           bigint PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    tenant_id        bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    stages           jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Actual work: the sum of every flushed task's wall-clock. Distinct from
    -- last_finished_at - first_started_at, which includes the human idle time
    -- between an upload and a manual re-run days later. Should agree with
    -- Σ self_ms; a drift means a span escaped the tree.
    compute_ms       bigint NOT NULL DEFAULT 0,
    -- Enqueue → worker pickup, accumulated across tasks. Invisible before this
    -- table; the first thing to check when "the app is slow" means "the worker
    -- is backed up".
    queue_wait_ms    bigint NOT NULL DEFAULT 0,
    -- Flush count = number of tasks that did work on this job. Lets a reader
    -- turn the accumulated totals back into per-run averages.
    runs             integer NOT NULL DEFAULT 0,
    first_started_at timestamptz NOT NULL DEFAULT now(),
    last_finished_at timestamptz NOT NULL DEFAULT now()
);

-- Tenant-scoped dashboards ("slowest jobs this week") sort by compute; the
-- composite keeps that a single index scan.
CREATE INDEX job_timings_tenant_compute_idx
    ON job_timings (tenant_id, compute_ms DESC);
CREATE INDEX job_timings_tenant_finished_idx
    ON job_timings (tenant_id, last_finished_at DESC);

-- Merges two stage maps: n, self_ms and total_ms add, max_ms takes the larger,
-- err adds, and meta prefers the newer side (engine/model can legitimately
-- change between runs — the last one that ran is the useful answer).
--
-- Accumulating rather than overwriting is deliberate: re-running "Apply MT" on
-- a job should show the total compute that job has consumed, and `runs` keeps
-- per-run averages recoverable from the totals.
--
-- Lives in SQL rather than a read-modify-write in Go so the upsert stays one
-- round trip and is atomic without SELECT ... FOR UPDATE. Two workers flushing
-- the same job concurrently (a translate task and a chained AI step) are
-- serialised by the row lock ON CONFLICT already takes.
-- +goose StatementBegin
CREATE FUNCTION merge_stage_timings(a jsonb, b jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE
AS $$
    SELECT coalesce(jsonb_object_agg(k, jsonb_strip_nulls(jsonb_build_object(
        'n',        coalesce((a -> k ->> 'n')::bigint, 0) + coalesce((b -> k ->> 'n')::bigint, 0),
        'self_ms',  coalesce((a -> k ->> 'self_ms')::bigint, 0) + coalesce((b -> k ->> 'self_ms')::bigint, 0),
        'total_ms', coalesce((a -> k ->> 'total_ms')::bigint, 0) + coalesce((b -> k ->> 'total_ms')::bigint, 0),
        -- Slowest single occurrence, measured inclusively: "how long did the
        -- worst one of these take end to end".
        'max_ms',   greatest(coalesce((a -> k ->> 'max_ms')::bigint, 0),
                             coalesce((b -> k ->> 'max_ms')::bigint, 0)),
        -- nullif + strip_nulls keeps the common zero-error case out of the blob
        'err',      nullif(coalesce((a -> k ->> 'err')::bigint, 0) + coalesce((b -> k ->> 'err')::bigint, 0), 0),
        'meta',     coalesce(b -> k -> 'meta', a -> k -> 'meta')
    ))), '{}'::jsonb)
    FROM (
        SELECT jsonb_object_keys(a) AS k
        UNION
        SELECT jsonb_object_keys(b) AS k
    ) keys;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS merge_stage_timings(jsonb, jsonb);
DROP TABLE IF EXISTS job_timings;
