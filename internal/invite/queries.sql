-- name: CreateInvite :one
INSERT INTO invites (tenant_id, email, role, invited_by, token, expires_at, kind, vendor_org_name)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetInviteByToken :one
SELECT * FROM invites WHERE token = $1;

-- name: GetPendingUnexpiredInviteByToken :one
-- Token-link paths only: the /invite/{token} link from the invite email honors
-- expires_at, unlike the email-matched auto-accept below. Pending = not yet
-- accepted; a revoked invite (DeleteInvite) is simply gone.
SELECT * FROM invites WHERE token = $1 AND accepted_at IS NULL AND expires_at > now()
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = invites.tenant_id AND t.status = 'deleting');

-- name: GetPendingInviteByEmailAndTenant :one
SELECT * FROM invites WHERE email = $1 AND tenant_id = $2 AND accepted_at IS NULL;

-- name: ListInvitesByTenant :many
-- Leaves the token out: only the invite email carries it. roles is the set the
-- caller may handle (invite.handledRoles); pending_only leaves out the
-- accepted invites, which ListAcceptedInvitesPage pages.
SELECT i.id, i.tenant_id, i.email, i.role, i.kind, i.vendor_org_name, i.invited_by,
       i.accepted_at, i.expires_at, i.created_at, u.name AS invited_by_name
FROM invites i
JOIN users u ON u.id = i.invited_by
WHERE i.tenant_id = @tenant_id AND i.role = ANY(@roles::text[])
  AND (NOT @pending_only::bool OR i.accepted_at IS NULL)
ORDER BY i.created_at DESC;

-- name: ListAcceptedInvitesPage :many
-- One page of the org's accepted invites the caller may handle (roles, as in
-- ListInvitesByTenant), newest acceptance first. q matches the email or the
-- vendor org's name as plain text. total counts every match.
SELECT i.id, i.tenant_id, i.email, i.role, i.kind, i.vendor_org_name, i.invited_by,
       i.accepted_at, i.expires_at, i.created_at, count(*) OVER () AS total
FROM invites i
WHERE i.tenant_id = @tenant_id AND i.accepted_at IS NOT NULL AND i.role = ANY(@roles::text[])
  AND (sqlc.narg('q')::text IS NULL
       OR strpos(lower(i.email), lower(sqlc.narg('q')::text)) > 0
       OR strpos(lower(coalesce(i.vendor_org_name, '')), lower(sqlc.narg('q')::text)) > 0)
ORDER BY i.accepted_at DESC, i.id DESC
LIMIT @lim OFFSET @off;

-- name: ResendInvite :one
-- Swaps in a new token and expiry, but only while the invite is pending and
-- still holds the token the caller read, so of two concurrent resends one wins.
UPDATE invites SET token = @new_token, expires_at = @expires_at
WHERE id = @id AND tenant_id = @tenant_id AND token = @old_token AND accepted_at IS NULL
RETURNING *;

-- name: LockPendingInviteForAccept :one
-- Internal: the accept flow, on an invite it already resolved from the
-- invitee's token or verified email. Accepting a vendor-org invite creates an
-- org: the row lock makes two concurrent accepts of one invite create it
-- once. No row = already accepted.
SELECT * FROM invites WHERE id = $1 AND accepted_at IS NULL FOR UPDATE;

-- name: AcceptInvite :one
UPDATE invites SET accepted_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = $1 AND tenant_id = $2;

-- name: GetPendingInvitesByEmail :many
-- Sign-in auto-match: any pending (un-accepted), non-revoked invite for this
-- email, across tenants. Deliberately NOT filtered by expires_at — a verified
-- identity plus an admin-created invite is authorization enough, so someone
-- added via the Vendors list can still sign in after the invite link would
-- have "expired". Expiry only guards the emailed token link
-- (GetPendingUnexpiredInviteByToken). Row present = allowed;
-- revoked (DeleteInvite) = gone.
-- An organization being deleted honours no invite.
SELECT * FROM invites WHERE email = $1 AND accepted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = invites.tenant_id AND t.status = 'deleting')
ORDER BY created_at DESC;

-- name: GetInviteTenant :one
-- Tenant-scoped single invite (revoke and resend).
SELECT * FROM invites WHERE id = $1 AND tenant_id = $2;
