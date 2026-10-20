-- ============================================================
-- Notifications
-- ============================================================

-- name: CreateNotification :one
INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateBellNotifications :exec
-- One bell-only notification for each of several users of one tenant, in one
-- insert (an issue resolved, for every manager of its org).
INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
SELECT @tenant_id, recipient, @event_type, @subject, @body_preview, @reference_type, @reference_id, false
FROM unnest(@user_ids::bigint[]) AS recipient;

-- name: ListNotifications :many
-- Pass 3 audit (Medium B): every read scopes by tenant_id even though
-- (user_id, tenant_id) is 1:1 today via the JWT — defense in depth so
-- that a future bug allowing user_id reuse across tenants (or a
-- compromised JWT with a guessable user_id) can't leak rows. Matches
-- the rest of the codebase's tenant-isolation invariant.
SELECT * FROM notifications
WHERE user_id = @user_id AND tenant_id = @tenant_id
  AND (@unread_only::boolean = false OR read = false)
  AND id < CASE WHEN @cursor::bigint = 0 THEN 9223372036854775807 ELSE @cursor END
ORDER BY id DESC
LIMIT @page_limit;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications
WHERE user_id = $1 AND tenant_id = $2 AND read = false;

-- name: MarkNotificationRead :exec
UPDATE notifications SET read = true WHERE id = $1 AND user_id = $2 AND tenant_id = $3;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read = true WHERE user_id = $1 AND tenant_id = $2 AND read = false;

-- name: UpdateNotificationEmailed :exec
-- Flips the emailed flag after a successful SendEmail. Lives separately from
-- CreateNotification so we can write the dedup row BEFORE the email goes out
-- (Service.Send invariant: row exists before email; eliminates the SendEmail-
-- success-then-row-fail → duplicate-email-next-hour race).
UPDATE notifications SET emailed = true WHERE id = $1;

-- name: GetNotificationPreferences :one
SELECT * FROM notification_preferences WHERE user_id = $1;

-- name: UpsertNotificationPreferences :one
INSERT INTO notification_preferences (user_id, email_assignment, email_rejection, email_completion, email_deadline, email_issue_updates)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET
  email_assignment = EXCLUDED.email_assignment,
  email_rejection = EXCLUDED.email_rejection,
  email_completion = EXCLUDED.email_completion,
  email_deadline = EXCLUDED.email_deadline,
  email_issue_updates = EXCLUDED.email_issue_updates,
  updated_at = now()
RETURNING *;

-- ============================================================
-- Org-level notification rules (admin master switches)
-- ============================================================

-- name: GetOrgNotificationRules :one
SELECT * FROM org_notification_rules WHERE tenant_id = $1;

-- name: UpsertOrgNotificationRules :one
INSERT INTO org_notification_rules (tenant_id, notify_assignment, notify_rejection, notify_completion, notify_deadline)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id) DO UPDATE SET
  notify_assignment = EXCLUDED.notify_assignment,
  notify_rejection = EXCLUDED.notify_rejection,
  notify_completion = EXCLUDED.notify_completion,
  notify_deadline = EXCLUDED.notify_deadline,
  updated_at = now()
RETURNING *;

-- ============================================================
-- Deadline queries
-- ============================================================

-- name: GetJobsApproachingDeadline :many
-- Internal: the hourly cron passes a NULL tenant_id and scans every org; the
-- manual trigger, an org owner/admin route, passes the caller's tenant_id.
-- tenant_id flows out through the projects JOIN so the handler tags
-- downstream notifications without a follow-up GetProjectByID round-trip.
--
-- Deadline semantics: the deadline lives on the ACTIVE step
-- assignment (jobs.current_step_id → job_step_assignments.deadline), and this
-- query reads that column and nothing else — a project's own date is never
-- substituted here. It can still be the value stored there: materialization
-- seeds an unscheduled step's assignment from the project deadline,
-- so such a task reminds like any other. The reminder
-- goes to the active step's assignee.
--
-- Uses idx_jsa_deadline (migration 046). Dedup is still keyed on the JOB id
-- (reference_type='job'), so the existing idx_notifications_deadline_dedup
-- (migration 027) and the downstream notification shape are unchanged.
--
-- The demo organization (tenants.kind = 'demo') is skipped:
-- its assignees are synthetic users who get no email.
SELECT j.id AS job_id,
       jsa.deadline AS deadline,
       j.file_id, j.target_language,
       f.name AS file_name, p.id AS project_id, p.name AS project_name,
       p.tenant_id AS tenant_id,
       jsa.assigned_to, ws.name AS step_name
FROM jobs j
JOIN job_step_assignments jsa ON jsa.id = j.current_step_id
JOIN workflow_steps ws ON ws.id = jsa.workflow_step_id
JOIN files f ON f.id = j.file_id
JOIN projects p ON p.id = f.project_id
JOIN tenants t ON t.id = p.tenant_id
WHERE jsa.deadline IS NOT NULL
  AND t.kind <> 'demo'
  AND (sqlc.narg(tenant_id)::bigint IS NULL OR p.tenant_id = sqlc.narg(tenant_id))
  AND p.deleted_at IS NULL
  AND jsa.deadline BETWEEN now() AND now() + interval '24 hours'
  AND NOT EXISTS (
    SELECT 1 FROM notifications n
    WHERE n.reference_id = j.id
      AND n.reference_type = 'job'
      AND n.event_type = 'deadline'
      AND n.created_at > now() - interval '20 hours'
  );

-- UpdateProjectDeadline removed in slice 4b — superseded by deadline field
-- in UpdateProjectMetadata (project/queries.sql). The standalone query was
-- only ever referenced in mock test stubs (never called from production)
-- since Pass 1 backend.
