-- Issues a user reports to the platform operators, and their conversation.
-- Member queries are scoped by tenant_id, and by reporter_id unless
-- @all_members (the viewer is a manager, who sees the whole org's issues).
-- They never read internal entries or their files. reporter_role is the
-- reporter's role in the issue's org now, empty once they have left it: who
-- may delete the issue depends on it. Operator queries read across tenants
-- and run only behind RequirePlatformAdmin.
--
-- Every list and get query returns the same columns: the issue, its org, the
-- reporter, the assignee's name, the original of a duplicate (its id and
-- status always; for a member, its title only when they may open it, which
-- duplicate_visible tells), and the reporter's role.

-- Operator queries leave out the issues of an organization being closed
-- (status 'deleting'): its data is deleted in the background, and nobody
-- should act on it or notify it meanwhile.

-- name: CreateIssue :one
INSERT INTO issues (tenant_id, reporter_id, kind, severity, area, title, description, context,
                    chat_verification, chat_verification_note, chat_verification_token_id)
VALUES (@tenant_id, @reporter_id, @kind, sqlc.narg('severity'), @area, @title, @description, @context,
        sqlc.narg('chat_verification'), sqlc.narg('chat_verification_note'), sqlc.narg('chat_verification_token_id'))
RETURNING *;

-- name: CreateIssueCapture :exec
-- Internal: only Service.Create, with the id of the issue it inserted for the
-- member's org in the same transaction. What the browser captured with it:
-- the action trail and the watch-mode recording.
INSERT INTO issue_captures (issue_id, trail, recording)
VALUES (@issue_id, sqlc.narg('trail'), sqlc.narg('recording'));

-- name: CountRecentIssuesByReporter :one
-- The per-user rate limit: issues this user filed since @since, in any org.
SELECT count(*) FROM issues WHERE reporter_id = @reporter_id AND created_at >= @since;

-- name: ListMemberIssuesNewest :many
-- The org's issues the viewer may see, most recently updated first (ties by
-- id). An empty filter list matches any value; a NULL search matches any
-- text, else a part of the title or description (wildcards escaped by the
-- caller). The cursor is the previous page's last (updated_at, id); a NULL
-- cursor_at is the first page.
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(CASE WHEN d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id) THEN d.title END, '')::text AS duplicate_title,
       d.status AS duplicate_status, COALESCE(d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id), false)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.tenant_id = @tenant_id AND (@all_members::boolean OR i.reporter_id = @viewer_id)
  AND i.deleted_at IS NULL
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND (sqlc.narg('search')::text IS NULL
       OR i.title ILIKE '%' || sqlc.narg('search')::text || '%'
       OR i.description ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL OR (i.updated_at, i.id) < (sqlc.narg('cursor_at')::timestamptz, @cursor_id::bigint))
ORDER BY i.updated_at DESC, i.id DESC
LIMIT @row_limit;

-- name: ListMemberIssuesOldest :many
-- ListMemberIssuesNewest in the other order: least recently updated first.
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(CASE WHEN d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id) THEN d.title END, '')::text AS duplicate_title,
       d.status AS duplicate_status, COALESCE(d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id), false)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.tenant_id = @tenant_id AND (@all_members::boolean OR i.reporter_id = @viewer_id)
  AND i.deleted_at IS NULL
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND (sqlc.narg('search')::text IS NULL
       OR i.title ILIKE '%' || sqlc.narg('search')::text || '%'
       OR i.description ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL OR (i.updated_at, i.id) > (sqlc.narg('cursor_at')::timestamptz, @cursor_id::bigint))
ORDER BY i.updated_at, i.id
LIMIT @row_limit;

-- name: GetMemberIssue :one
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(CASE WHEN d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id) THEN d.title END, '')::text AS duplicate_title,
       d.status AS duplicate_status, COALESCE(d.tenant_id = i.tenant_id AND (@all_members::boolean OR d.reporter_id = @viewer_id), false)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.id = @id AND i.tenant_id = @tenant_id AND (@all_members::boolean OR i.reporter_id = @viewer_id)
  AND i.deleted_at IS NULL;

-- name: LockMemberIssue :one
-- The issue row, locked for a member comment's status change.
SELECT * FROM issues
WHERE id = @id AND tenant_id = @tenant_id AND (@all_members::boolean OR reporter_id = @viewer_id)
  AND deleted_at IS NULL
FOR UPDATE;

-- name: ListMemberIssueComments :many
-- The conversation without the support team's internal entries.
SELECT c.id, c.issue_id, c.author_id, c.author_side, c.body, c.status, c.internal,
       c.field, c.from_value, c.to_value, c.created_at,
       COALESCE(u.name, '') AS author_name
FROM issue_comments c
JOIN issues i ON i.id = c.issue_id
LEFT JOIN users u ON u.id = c.author_id
WHERE c.issue_id = @issue_id AND i.tenant_id = @tenant_id
  AND (@all_members::boolean OR i.reporter_id = @viewer_id)
  AND i.deleted_at IS NULL AND c.deleted_at IS NULL AND NOT c.internal
ORDER BY c.created_at, c.id;

-- name: CountRecentMemberComments :one
-- The per-member comment limit: comments this user wrote on the org's side
-- since @since, in any org.
SELECT count(*) FROM issue_comments
WHERE author_id = @author_id AND author_side = 'reporter' AND created_at >= @since;

-- name: CreateIssueComment :one
-- Internal: callers load the issue through a scoped or operator query first.
INSERT INTO issue_comments (issue_id, author_id, author_side, body, status, internal, field, from_value, to_value)
VALUES (@issue_id, sqlc.narg('author_id'), @author_side, sqlc.narg('body'), sqlc.narg('status'), @internal,
        sqlc.narg('field'), sqlc.narg('from_value'), sqlc.narg('to_value'))
RETURNING *;

-- name: UpdateIssueActivity :one
-- Internal: callers load and lock the issue first. A visible entry: bumps
-- updated_at, sets who acted last when @last_activity_by is non-NULL, and,
-- when @status is non-NULL, the new status. A new status clears the
-- confirmation and sets the original (@duplicate_of_id, for duplicate only);
-- resolved_at follows the status.
UPDATE issues SET
    status = COALESCE(sqlc.narg('status'), status),
    resolved_at = CASE
        WHEN sqlc.narg('status')::text IN ('resolved', 'wont_fix', 'duplicate') THEN now()
        WHEN sqlc.narg('status')::text IS NOT NULL THEN NULL
        ELSE resolved_at END,
    duplicate_of_id = CASE
        WHEN sqlc.narg('status')::text IS NULL THEN duplicate_of_id
        ELSE sqlc.narg('duplicate_of_id')::bigint END,
    confirmed_at = CASE WHEN sqlc.narg('status')::text IS NULL THEN confirmed_at END,
    last_activity_by = COALESCE(sqlc.narg('last_activity_by'), last_activity_by),
    updated_at = now()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: RepointDuplicates :many
-- Internal: platform operators only (RequirePlatformAdmin); the caller locked
-- @id. The duplicates of an issue that becomes a duplicate itself follow it to
-- @original_id, so no duplicate points at another duplicate.
UPDATE issues SET duplicate_of_id = @original_id::bigint, updated_at = now()
WHERE duplicate_of_id = @id::bigint AND deleted_at IS NULL
RETURNING id;

-- name: ReopenDuplicatesOf :exec
-- Internal: the caller soft-deleted @original_id and holds
-- LockIssueDuplicates. Its duplicates go back to the support team's queue
-- (reopen_issue_duplicates, which a hard delete runs through a trigger).
SELECT reopen_issue_duplicates(ARRAY(
    SELECT id FROM issues
    WHERE duplicate_of_id = @original_id::bigint AND status = 'duplicate' AND deleted_at IS NULL)::bigint[]);

-- name: ConfirmIssueFixed :one
-- Internal: callers load and lock the issue through a scoped query first.
UPDATE issues SET confirmed_at = now(), last_activity_by = 'reporter', updated_at = now()
WHERE id = @id AND status = 'resolved' AND confirmed_at IS NULL AND deleted_at IS NULL
RETURNING *;

-- name: CreateIssueAttachment :one
-- Internal: callers load the issue through a scoped or operator query first.
INSERT INTO issue_attachments (issue_id, comment_id, filename, content_type, size_bytes, storage_key)
VALUES (@issue_id, sqlc.narg('comment_id'), @filename, @content_type, @size_bytes, @storage_key)
RETURNING *;

-- name: ListIssueAttachments :many
-- Internal: platform operators only (RequirePlatformAdmin); callers load the
-- issue through an operator query first. Includes internal notes' files.
SELECT * FROM issue_attachments WHERE issue_id = @issue_id ORDER BY id;

-- name: ListMemberIssueAttachments :many
-- Internal: callers load the issue through a member query first. The files of
-- the report and of the entries the org sees.
SELECT a.* FROM issue_attachments a
LEFT JOIN issue_comments c ON c.id = a.comment_id
WHERE a.issue_id = @issue_id AND (a.comment_id IS NULL OR NOT c.internal)
ORDER BY a.id;

-- name: GetIssueAttachment :one
-- Internal: platform operators only (RequirePlatformAdmin); the attachment
-- must belong to the issue the caller loaded.
SELECT * FROM issue_attachments WHERE id = @id AND issue_id = @issue_id;

-- name: GetMemberIssueAttachment :one
-- Internal: callers load the issue through a member query first. Never a file
-- of an internal note.
SELECT a.* FROM issue_attachments a
LEFT JOIN issue_comments c ON c.id = a.comment_id
WHERE a.id = @id AND a.issue_id = @issue_id AND (a.comment_id IS NULL OR NOT c.internal);

-- name: SetIssueDescription :one
-- Internal: only Service.Create, in the transaction that inserted the issue:
-- the description with its inline images pointing at the stored files.
UPDATE issues SET description = @description
WHERE id = @id
RETURNING *;

-- name: SetIssueCommentBody :exec
-- Internal: only in the transaction that inserted the comment: its text with
-- its inline images pointing at the stored files.
UPDATE issue_comments SET body = @body
WHERE id = @id;

-- name: UpdateIssueTriage :one
-- Internal: platform operators (RequirePlatformAdmin), and the reporter's
-- kind change after LockMemberIssue. Severity stays NULL for anything but a
-- bug (a table check). The org's lists do not move.
UPDATE issues SET kind = @kind, severity = sqlc.narg('severity'), area = @area
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteMemberIssue :execrows
-- The caller has already been checked against the issue's reporter.
UPDATE issues SET deleted_at = now()
WHERE id = @id AND tenant_id = @tenant_id AND deleted_at IS NULL;

-- name: SoftDeleteIssueForPlatform :execrows
-- Internal: platform operators only (RequirePlatformAdmin). Any org's issue.
UPDATE issues SET deleted_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: SetIssuePriority :one
-- Internal: platform operators only (RequirePlatformAdmin). Priority is the
-- support team's own field, so the org's lists do not move.
UPDATE issues SET priority = sqlc.narg('priority')
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SetIssueTags :one
-- Internal: platform operators only (RequirePlatformAdmin). Tags are the
-- support team's own field, so the org's lists do not move.
UPDATE issues SET tags = @tags::text[]
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: SetIssueAssignee :one
-- Internal: platform operators only (RequirePlatformAdmin). The org sees the
-- assignee, so the issue counts as updated.
UPDATE issues SET assignee_id = sqlc.narg('assignee_id'), updated_at = now()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: ListUsersByEmails :many
-- Internal: platform operators only (RequirePlatformAdmin). The accounts of
-- the support team (PLATFORM_ADMIN_EMAILS, lower-cased by the caller).
SELECT id, name, email FROM users WHERE lower(email) = ANY(@emails::text[]) ORDER BY name, email, id;

-- name: ListUserNames :many
-- Internal: the names of the support team members an issue's history names.
SELECT id, name FROM users WHERE id = ANY(@ids::bigint[]);

-- name: ListIssuesForPlatformNewest :many
-- Internal: platform operators only (RequirePlatformAdmin). Every org's issues,
-- paged and searched like ListMemberIssuesNewest. An empty filter list matches
-- any value; a severity filter matches bugs only; a tag filter matches an issue
-- with any of the tags. The assignee filter matches the listed assignees, plus
-- the unassigned issues when @unassigned. A verification filter matches the
-- reports the assistant checked with one of those results.
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(d.title, '')::text AS duplicate_title,
       d.status AS duplicate_status, (d.id IS NOT NULL)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.deleted_at IS NULL AND t.status <> 'deleting'
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND (cardinality(@tenant_ids::bigint[]) = 0 OR i.tenant_id = ANY(@tenant_ids::bigint[]))
  AND (cardinality(@priorities::text[]) = 0 OR i.priority = ANY(@priorities::text[]))
  AND (cardinality(@tags::text[]) = 0 OR i.tags && @tags::text[])
  AND ((cardinality(@assignee_ids::bigint[]) = 0 AND NOT @unassigned::boolean)
       OR i.assignee_id = ANY(@assignee_ids::bigint[])
       OR (@unassigned::boolean AND i.assignee_id IS NULL))
  AND (cardinality(@verifications::text[]) = 0 OR i.chat_verification = ANY(@verifications::text[]))
  AND (sqlc.narg('search')::text IS NULL
       OR i.title ILIKE '%' || sqlc.narg('search')::text || '%'
       OR i.description ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL OR (i.updated_at, i.id) < (sqlc.narg('cursor_at')::timestamptz, @cursor_id::bigint))
ORDER BY i.updated_at DESC, i.id DESC
LIMIT @row_limit;

-- name: ListIssuesForPlatformOldest :many
-- Internal: platform operators only (RequirePlatformAdmin).
-- ListIssuesForPlatformNewest in the other order.
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(d.title, '')::text AS duplicate_title,
       d.status AS duplicate_status, (d.id IS NOT NULL)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.deleted_at IS NULL AND t.status <> 'deleting'
  AND (cardinality(@statuses::text[]) = 0 OR i.status = ANY(@statuses::text[]))
  AND (cardinality(@kinds::text[]) = 0 OR i.kind = ANY(@kinds::text[]))
  AND (cardinality(@areas::text[]) = 0 OR i.area = ANY(@areas::text[]))
  AND (cardinality(@severities::text[]) = 0 OR i.severity = ANY(@severities::text[]))
  AND (cardinality(@tenant_ids::bigint[]) = 0 OR i.tenant_id = ANY(@tenant_ids::bigint[]))
  AND (cardinality(@priorities::text[]) = 0 OR i.priority = ANY(@priorities::text[]))
  AND (cardinality(@tags::text[]) = 0 OR i.tags && @tags::text[])
  AND ((cardinality(@assignee_ids::bigint[]) = 0 AND NOT @unassigned::boolean)
       OR i.assignee_id = ANY(@assignee_ids::bigint[])
       OR (@unassigned::boolean AND i.assignee_id IS NULL))
  AND (cardinality(@verifications::text[]) = 0 OR i.chat_verification = ANY(@verifications::text[]))
  AND (sqlc.narg('search')::text IS NULL
       OR i.title ILIKE '%' || sqlc.narg('search')::text || '%'
       OR i.description ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL OR (i.updated_at, i.id) > (sqlc.narg('cursor_at')::timestamptz, @cursor_id::bigint))
ORDER BY i.updated_at, i.id
LIMIT @row_limit;

-- name: ListIssueOrgsForPlatform :many
-- Internal: platform operators only (RequirePlatformAdmin). The orgs that have
-- issues, for the operators' organization filter.
SELECT DISTINCT t.id, t.name
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
WHERE i.deleted_at IS NULL AND t.status <> 'deleting'
ORDER BY t.name, t.id;

-- name: ListIssueTagsForPlatform :many
-- Internal: platform operators only (RequirePlatformAdmin). Every tag in use,
-- for the tag filter and the tag picker.
SELECT DISTINCT tag::text AS tag
FROM issues, unnest(issues.tags) AS tag
WHERE issues.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = issues.tenant_id AND t.status = 'deleting')
ORDER BY 1;

-- name: GetIssueForPlatform :one
-- Internal: platform operators only (RequirePlatformAdmin).
SELECT i.*, t.name AS org_name, u.name AS reporter_name, u.email AS reporter_email,
       a.name AS assignee_name, d.id AS duplicate_id, COALESCE(d.title, '')::text AS duplicate_title,
       d.status AS duplicate_status, (d.id IS NOT NULL)::boolean AS duplicate_visible,
       COALESCE(rm.role, '')::text AS reporter_role
FROM issues i
JOIN tenants t ON t.id = i.tenant_id
JOIN users u ON u.id = i.reporter_id
LEFT JOIN memberships rm ON rm.user_id = i.reporter_id AND rm.tenant_id = i.tenant_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN issues d ON d.id = i.duplicate_of_id AND d.deleted_at IS NULL
WHERE i.id = @id AND i.deleted_at IS NULL AND t.status <> 'deleting';

-- name: ListIssueDuplicatesForPlatform :many
-- Internal: platform operators only (RequirePlatformAdmin). The issues the
-- support team marked as duplicates of @original_id, in any org, oldest first.
SELECT issues.id, issues.title, issues.status FROM issues
WHERE issues.duplicate_of_id = @original_id::bigint AND issues.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = issues.tenant_id AND t.status = 'deleting')
ORDER BY id;

-- name: GetIssueCaptureForPlatform :one
-- Internal: platform operators only (RequirePlatformAdmin). The trail and the
-- recording of one issue; no row when its report carried neither.
SELECT trail, recording FROM issue_captures WHERE issue_id = @issue_id;

-- name: LockIssueForPlatform :one
-- Internal: platform operators only (RequirePlatformAdmin).
SELECT * FROM issues WHERE issues.id = @id AND issues.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = issues.tenant_id AND t.status = 'deleting')
FOR UPDATE;

-- name: LockIssueDuplicates :exec
-- Internal: platform operators only (RequirePlatformAdmin). Marking a
-- duplicate reads its original and moves the issue's own duplicates, so the
-- requests that mark one run one at a time, until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended('issue_duplicates', 0));

-- name: ListIssueCommentsForPlatform :many
-- Internal: platform operators only (RequirePlatformAdmin). Includes the
-- internal entries.
SELECT c.id, c.issue_id, c.author_id, c.author_side, c.body, c.status, c.internal,
       c.field, c.from_value, c.to_value, c.created_at,
       COALESCE(u.name, '') AS author_name
FROM issue_comments c
LEFT JOIN users u ON u.id = c.author_id
WHERE c.issue_id = @issue_id AND c.deleted_at IS NULL
ORDER BY c.created_at, c.id;

-- name: ListTenantManagerIDs :many
-- The org's owners, admins and project managers: who hears that one of the
-- org's issues was resolved.
SELECT user_id FROM memberships
WHERE tenant_id = @tenant_id AND role IN ('owner', 'admin', 'project_manager')
ORDER BY user_id;

-- name: GetIssueByImportKey :one
-- Internal: the import command, run by the deployment's operator. The issue an
-- earlier import made from the same source, in the reporter's org.
SELECT id FROM issues WHERE tenant_id = @tenant_id AND import_key = @import_key;

-- name: ImportIssue :one
-- Internal: the import command, run by the deployment's operator. Returns no
-- row when an earlier import already made this issue.
INSERT INTO issues (tenant_id, reporter_id, kind, severity, area, title, description, context, status,
                    priority, tags, assignee_id, last_activity_by, resolved_at, confirmed_at, import_key)
VALUES (@tenant_id, @reporter_id, @kind, sqlc.narg('severity'), @area, @title, @description, @context, @status,
        sqlc.narg('priority'), @tags::text[], sqlc.narg('assignee_id'), @last_activity_by,
        CASE WHEN @status::text IN ('resolved', 'wont_fix', 'duplicate') THEN now() END,
        CASE WHEN @confirmed::boolean THEN now() END, @import_key)
ON CONFLICT (tenant_id, import_key) WHERE import_key IS NOT NULL DO NOTHING
RETURNING *;

-- name: ListIssueAttachmentBlobPurges :many
-- Internal: platform sweep across tenants. One batch of queued attachment keys.
SELECT storage_key FROM issue_attachment_blob_purges
ORDER BY queued_at, storage_key
LIMIT @batch_size;

-- name: DeleteIssueAttachmentBlobPurges :exec
-- Internal: platform sweep across tenants. Called after the files are gone.
DELETE FROM issue_attachment_blob_purges WHERE storage_key = ANY(@storage_keys::text[]);
