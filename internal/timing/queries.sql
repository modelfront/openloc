-- name: UpsertJobTiming :exec
-- Merge one task's accumulated stage timings into the job's single row.
-- Accumulating rather than overwriting is deliberate: re-running
-- "Apply MT" on a job should show the total compute that job has consumed, and
-- `runs` keeps per-run averages recoverable from the totals.
--
-- Both ends of the wall-clock span come from the database clock: the caller
-- reports how long it ran (compute_ms) and the start is derived from now() minus
-- that lifetime. A caller-supplied absolute start would be a different clock
-- from now() — Docker's VM drifts tens of ms, and separate workers don't agree
-- with each other at all — which could store a span ending before it began.
-- first_started_at then takes the earliest of the two so
-- the span stays honest when tasks land out of order.
INSERT INTO job_timings (
    job_id, tenant_id, stages, compute_ms, queue_wait_ms, runs,
    first_started_at, last_finished_at
)
VALUES ($1, $2, $3, $4, $5, 1, now() - ($4::bigint * interval '1 millisecond'), now())
ON CONFLICT (job_id) DO UPDATE SET
    stages           = merge_stage_timings(job_timings.stages, EXCLUDED.stages),
    compute_ms       = job_timings.compute_ms + EXCLUDED.compute_ms,
    queue_wait_ms    = job_timings.queue_wait_ms + EXCLUDED.queue_wait_ms,
    runs             = job_timings.runs + 1,
    first_started_at = LEAST(job_timings.first_started_at, EXCLUDED.first_started_at),
    last_finished_at = now();

-- name: GetJobTiming :one
-- Tenant-scoped: job_timings carries its own tenant_id, so the breakdown for a
-- job can be fetched without joining back through files → projects.
SELECT * FROM job_timings WHERE job_id = $1 AND tenant_id = $2;

-- name: ListJobTimingsByProject :many
-- Tenant-scoped via the projects join. Powers the per-project "where is time
-- going" panel without N+1 fetching each job's row.
--
-- Carries the job's display identity (file name + target language) so the panel
-- can label rows without a second round trip — a timing row on its own is just
-- an opaque job id to a human.
SELECT jt.job_id, jt.tenant_id, jt.stages, jt.compute_ms, jt.queue_wait_ms,
       jt.runs, jt.first_started_at, jt.last_finished_at,
       j.target_language, j.status AS job_status, j.word_count,
       f.name AS file_name, f.id AS file_id
FROM job_timings jt
JOIN jobs j ON j.id = jt.job_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
WHERE f.project_id = $1 AND p.tenant_id = $2 AND p.deleted_at IS NULL AND jt.tenant_id = $2
ORDER BY jt.compute_ms DESC;
