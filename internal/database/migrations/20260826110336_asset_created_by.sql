-- +goose Up
-- Creator attribution for TMs + term bases, so the list pages can answer
-- "who owns this?" without digging through history. `projects.created_by`
-- already exists (migration 025); this brings the other two user-created
-- assets to parity.
--
-- Nullable + ON DELETE SET NULL, matching projects.created_by: an asset must
-- outlive the user who made it, and pre-existing rows have no creator to
-- record. No index — both tables hold tens of rows per tenant and their
-- creator filter runs client-side.
ALTER TABLE tm_collections ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE term_bases     ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;

-- Defensive backfill of projects.created_by from the project.created audit
-- event (internal/projectevents).
--
-- This matches ZERO rows today, deliberately kept anyway. Two reasons it can't
-- fire right now:
--   - project.Create writes the same actor into both places (see
--     internal/project/create.go — the column and the event get one
--     auth.UserIDPtr(userID) value), so a NULL created_by implies a NULL
--     actor_id, which the IS NOT NULL filter below excludes.
--   - Projects older than the audit log itself (project_events merged
--     2026-08-25) have no event row at all — including the pre-migration-025
--     projects whose created_by was never populated. Those are unrecoverable.
-- It stays as insurance against a future create path that emits an attributed
-- event but leaves the column NULL. Safe to keep: it never overwrites a
-- non-NULL created_by, so re-running can't corrupt real attribution.
--
-- DISTINCT ON picks the earliest event per project (id order == insert order).
UPDATE projects p
SET    created_by = e.actor_id
FROM (
  SELECT DISTINCT ON (project_id) project_id, actor_id
  FROM   project_events
  WHERE  event_type = 'project.created' AND actor_id IS NOT NULL
  ORDER  BY project_id, id
) e
WHERE e.project_id = p.id AND p.created_by IS NULL;

-- +goose Down
ALTER TABLE tm_collections DROP COLUMN IF EXISTS created_by;
ALTER TABLE term_bases     DROP COLUMN IF EXISTS created_by;
-- The projects backfill is deliberately NOT reverted: a backfilled value is
-- indistinguishable from a natively-set one, so undoing it would throw away
-- real attribution.
