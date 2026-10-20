-- +goose Up
-- Polymorphic-ref cleanup for notifications.
--
-- The `notifications` table carries `(reference_type, reference_id)` to
-- point at any of several entity types — today only `'job'` and
-- `'project'` are produced by `notification.Service.Send`, but the
-- design is intentionally polymorphic so future event types can
-- reference any entity without schema changes.
--
-- Polymorphic refs can't use a SQL FOREIGN KEY (no single referenced
-- table), so when a job or project is deleted, its notifications used
-- to stay around as dangling pointers — accumulating in user
-- notification queues, polluting test runs (J27 caught this — 6
-- accumulated rows from prior test runs surfaced during anti-tautology
-- review), and showing as un-clickable rows in the bell.
--
-- Architecture decision:
--   BEFORE DELETE triggers on `jobs` and `projects` clean matching
--   notifications. Both triggers share one plpgsql function
--   parameterised via TG_ARGV (DRY). Cascade chains
--   (project → files → segments → jobs) automatically fire the
--   per-row trigger on each cascaded job, so a project delete cleans
--   notifications for both the project AND its jobs in one step.
--
-- Why triggers (not app-level DELETE in handlers):
--   - Existing `notifications.tenant_id`/`user_id` FKs use ON DELETE
--     CASCADE — DB-enforced cleanup is the established pattern.
--   - Triggers fire for ANY delete path (HTTP handler, raw SQL,
--     future cron cleanup, test fixtures, dev tooling) — no drift
--     risk if a new caller is added.
--   - One source of truth at the schema level matches the existing
--     CASCADE FK posture.
--
-- Why not polymorphic FK columns (notification_job + notification_project):
--   - Heavyweight schema split for a problem that triggers solve
--     in 20 lines.
--   - Locks in the specific reference types — defeats the polymorphic
--     intent of the original design.
--
-- Known limitations:
--   - TRUNCATE TABLE jobs/projects does NOT fire BEFORE DELETE
--     triggers (PG fires TRUNCATE triggers separately). Production
--     code never TRUNCATEs these tables — only test fixtures do, and
--     they re-create the schema cleanly. If a future ops runbook adds
--     TRUNCATE, add a TRUNCATE trigger or sweep notifications first.
--   - The one-time DELETE below scans the notifications table once.
--     At MVP scale (~thousands of rows) this is sub-millisecond. At
--     production scale with millions of dangling rows, the operation
--     could hold a row-lock window long enough to matter for a hot
--     deploy. A future migration could chunk the sweep with a LIMIT
--     loop if scale demands; pre-emptive chunking now would be
--     premature.

-- One-time cleanup of EXISTING dangling notifications. The triggers
-- below only catch NEW deletes — historical rows pointing at
-- already-deleted jobs/projects need a sweep here. Down migration
-- doesn't restore these (they're gone forever); that's fine — the
-- Down is for schema reversal, not data reversal.
DELETE FROM notifications
WHERE (reference_type = 'job'     AND reference_id NOT IN (SELECT id FROM jobs))
   OR (reference_type = 'project' AND reference_id NOT IN (SELECT id FROM projects));

-- Index supporting the trigger's point-lookup predicate
-- `WHERE reference_type = X AND reference_id = Y`. Without it, every
-- per-row trigger firing during a project cascade scans the full
-- notifications table — a project with 50 cascaded jobs becomes 50
-- seq scans. The existing `idx_notifications_deadline_dedup` is
-- partial (`WHERE event_type='deadline' AND reference_type='job'`)
-- and only helps deadline-cron lookups; this index covers ALL
-- (reference_type, reference_id) point lookups for any event_type.
--
-- Column order: (reference_type, reference_id) — leading on
-- reference_type leaves room for future "list all notifications for
-- type X" queries to use the same index, while equality on both
-- columns matches the trigger's exact predicate.
CREATE INDEX idx_notifications_reference
    ON notifications (reference_type, reference_id);

-- The shared trigger function. TG_ARGV[0] carries the reference_type
-- string ('job' or 'project') so one function body serves both
-- triggers. STABLE/STRICT not appropriate here (function has side
-- effects); default volatility (VOLATILE) is correct.
--
-- The StatementBegin/End directives below are mandatory because goose
-- splits SQL on semicolons by default, which would shred the plpgsql
-- body's internal semicolons. The directive treats the block as one
-- atomic statement.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cleanup_notifications_on_delete()
RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM notifications
    WHERE reference_type = TG_ARGV[0]
      AND reference_id   = OLD.id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER cleanup_job_notifications
    BEFORE DELETE ON jobs
    FOR EACH ROW
    EXECUTE FUNCTION cleanup_notifications_on_delete('job');

CREATE TRIGGER cleanup_project_notifications
    BEFORE DELETE ON projects
    FOR EACH ROW
    EXECUTE FUNCTION cleanup_notifications_on_delete('project');

-- +goose Down
DROP TRIGGER IF EXISTS cleanup_project_notifications ON projects;
DROP TRIGGER IF EXISTS cleanup_job_notifications ON jobs;
DROP FUNCTION IF EXISTS cleanup_notifications_on_delete();
DROP INDEX IF EXISTS idx_notifications_reference;
