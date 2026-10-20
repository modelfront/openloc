-- +goose Up
-- +goose StatementBegin

-- Pass 4 slice 4b — partial index on projects.deadline so the deadline
-- cron's project-fallback branch can hit an index instead of seq-scanning
-- projects.
--
-- Mirrors `idx_jobs_deadline` from migration 001. Partial WHERE clause
-- keeps the index small — projects without deadlines are the common case
-- and should never be in the index.
--
-- Used by GetJobsApproachingDeadline's second OR branch
-- (`p.deadline IS NOT NULL AND p.deadline BETWEEN now() AND now()+24h`).
-- At MVP scale this is overkill, but it costs nothing now and prevents
-- a later "why is the cron slow" investigation when the projects table
-- grows past a few thousand rows.
--
-- Anti-tautology: drop this index, run EXPLAIN on the cron query against
-- a projects table with 10k+ rows; second branch shifts from "Index Scan
-- using idx_projects_deadline" to "Seq Scan on projects".

CREATE INDEX idx_projects_deadline
    ON projects (deadline)
    WHERE deadline IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_projects_deadline;

-- +goose StatementEnd
