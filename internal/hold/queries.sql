-- name: GetHoldTarget :one
-- Tenant- and job-scoped: the translation, only when it belongs to the job in
-- the route (the job auth.RequireJobOwnership / linguist access authorised).
SELECT t.*
FROM translations t
JOIN segments s ON s.id = t.segment_id
JOIN jobs j ON j.file_id = s.file_id AND j.target_language = t.target_language
JOIN files f ON f.id = s.file_id
JOIN projects p ON p.id = f.project_id
WHERE t.id = @translation_id AND j.id = @job_id AND p.tenant_id = @tenant_id AND p.deleted_at IS NULL;

-- name: MoveTabHold :exec
-- A tab holds one segment at a time: taking a new one drops the tab's others.
DELETE FROM translation_holds
WHERE tenant_id = @tenant_id AND user_id = @user_id AND tab_id = @tab_id
  AND translation_id <> @translation_id;

-- name: AcquireHold :one
-- Takes or renews the hold. Succeeds when the row is free, expired, or already
-- held by the same user (another of their tabs hands it over); otherwise the
-- WHERE is false and no row comes back. acquired_at stays put on a renewal.
INSERT INTO translation_holds (translation_id, tenant_id, job_id, user_id, tab_id, acquired_at, expires_at)
VALUES (@translation_id, @tenant_id, @job_id, @user_id, @tab_id, now(),
        now() + make_interval(secs => @ttl_seconds::int))
ON CONFLICT (translation_id) DO UPDATE
SET tab_id      = EXCLUDED.tab_id,
    user_id     = EXCLUDED.user_id,
    acquired_at = CASE WHEN translation_holds.user_id = EXCLUDED.user_id
                        AND translation_holds.expires_at > now()
                       THEN translation_holds.acquired_at ELSE now() END,
    expires_at  = EXCLUDED.expires_at
WHERE translation_holds.expires_at <= now()
   OR translation_holds.user_id = EXCLUDED.user_id
RETURNING translation_id, user_id, tab_id, acquired_at, expires_at;

-- name: TrimUserJobHolds :exec
-- A user keeps at most @keep holds in a job: the ones renewed most recently.
DELETE FROM translation_holds d
WHERE d.tenant_id = @tenant_id AND d.job_id = @job_id AND d.user_id = @user_id
  AND d.translation_id NOT IN (
      SELECT h.translation_id FROM translation_holds h
      WHERE h.tenant_id = @tenant_id AND h.job_id = @job_id AND h.user_id = @user_id
      ORDER BY h.expires_at DESC
      LIMIT @keep::int);

-- name: GetLiveHolder :one
-- Tenant-scoped: who holds the translation now, for the refusal message.
SELECT h.user_id, COALESCE(NULLIF(u.name, ''), u.email) AS holder_name, h.expires_at
FROM translation_holds h
JOIN users u ON u.id = h.user_id
WHERE h.translation_id = @translation_id AND h.tenant_id = @tenant_id
  AND h.expires_at > now();

-- name: ReleaseTabHold :exec
-- Deletes the caller's hold from one tab; a hold another tab took over stays.
DELETE FROM translation_holds
WHERE tenant_id = @tenant_id AND translation_id = @translation_id
  AND user_id = @user_id AND tab_id = @tab_id;

-- name: ReleaseUserHold :exec
-- Deletes the caller's hold on a translation from any tab: the task stopped
-- being editable for them.
DELETE FROM translation_holds
WHERE tenant_id = @tenant_id AND translation_id = @translation_id AND user_id = @user_id;

-- name: ListJobHolds :many
-- Tenant-scoped: the live holds in a job, with the holders' names.
SELECT h.translation_id, h.user_id, COALESCE(NULLIF(u.name, ''), u.email) AS holder_name,
       h.acquired_at, h.expires_at
FROM translation_holds h
JOIN users u ON u.id = h.user_id
WHERE h.job_id = @job_id AND h.tenant_id = @tenant_id AND h.expires_at > now()
ORDER BY h.translation_id;

-- name: DeleteUserHoldsInTenant :exec
-- A member who leaves the organization releases every segment they hold there.
DELETE FROM translation_holds WHERE tenant_id = @tenant_id AND user_id = @user_id;

-- name: DeleteExpiredHolds :execrows
-- Internal: periodic sweep, every tenant. A live hold is never touched.
DELETE FROM translation_holds WHERE expires_at < now() - interval '1 minute';
