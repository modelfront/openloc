-- +goose Up
-- +goose StatementBegin

-- Pass 4 slice 4a — index for the deadline cron's NOT EXISTS dedup subquery.
--
-- Without this index, GetJobsApproachingDeadline does a seq scan on the
-- `notifications` table for every row in the outer loop of the hourly
-- check. The existing `idx_notifications_user_*` indexes are keyed on
-- `user_id` and don't cover `(reference_id, reference_type, event_type,
-- created_at)` — which is what the dedup subquery actually filters on.
--
-- Partial index: `WHERE event_type = 'deadline' AND reference_type = 'job'`
-- keeps the index small. Deadline notifications are a tiny slice of the
-- overall notifications table (most are assignment/rejection/completion).
-- This is the only place reference_type+event_type is queried; other
-- callers filter by user_id which the existing indexes cover.
--
-- Column order: (reference_id, created_at DESC). The query uses
-- `n.reference_id = j.id AND … AND n.created_at > now() - interval '20 hours'`,
-- so leading on reference_id makes the equality match the index prefix and
-- the created_at predicate bounds the scan.
--
-- DESC on created_at because the predicate is "more recent than X" — DESC
-- order lets Postgres stop scanning early once it walks past the boundary.
--
-- Anti-tautology: drop this index, run the cron against a notifications
-- table with 100k+ rows; EXPLAIN should show "Seq Scan on notifications"
-- and runtime degrades linearly. With the index, plan stays "Index Scan".

CREATE INDEX idx_notifications_deadline_dedup
    ON notifications (reference_id, created_at DESC)
    WHERE event_type = 'deadline' AND reference_type = 'job';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_notifications_deadline_dedup;

-- +goose StatementEnd
