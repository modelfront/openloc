-- name: ClaimInactivityWarnings :many
-- Internal: the inactivity sweep. Claims, and returns, the enabled vendor org
-- memberships unused since @unused_since that have no warning yet and no live
-- claim (one made after @claims_expire_before). A vendor org, or one whose
-- client, is not approved is left alone: its people cannot use it, so signing
-- in would not help them. The warning itself is set only once the email went
-- out (MarkInactivityWarned); a claim whose email never did simply expires.
WITH due AS (
  SELECT m.id FROM memberships m
  JOIN tenants t ON t.id = m.tenant_id AND t.kind = 'vendor' AND t.status = 'approved'
  JOIN tenants c ON c.id = t.client_tenant_id AND c.status = 'approved'
  JOIN users u ON u.id = m.user_id AND u.is_active
  WHERE m.disabled_at IS NULL AND m.inactivity_warned_at IS NULL
    AND (m.inactivity_warning_claimed_at IS NULL OR m.inactivity_warning_claimed_at < @claims_expire_before::timestamptz)
    AND m.last_active_at < @unused_since::timestamptz
  ORDER BY m.last_active_at, m.id
  LIMIT @lim
  FOR UPDATE OF m SKIP LOCKED
)
UPDATE memberships m SET inactivity_warning_claimed_at = now()
FROM due, tenants t, users u
WHERE m.id = due.id AND t.id = m.tenant_id AND u.id = m.user_id
RETURNING m.id, m.tenant_id, m.user_id, m.last_active_at, u.email, u.name, t.name AS tenant_name;

-- name: MarkInactivityWarned :exec
-- Internal: the warning email went out; its days start now. Only while the
-- claim holds: a use of the org (or an enable) while the email was on its way
-- withdrew it, and their new days count without a warning.
UPDATE memberships SET inactivity_warned_at = now(), inactivity_warning_claimed_at = NULL
WHERE id = $1 AND inactivity_warning_claimed_at IS NOT NULL AND inactivity_warned_at IS NULL;

-- name: ReleaseInactivityClaims :exec
-- Internal: claims whose email was not sent (it failed, or the sweep stopped
-- first), so the next sweep tries again without waiting for them to expire.
UPDATE memberships SET inactivity_warning_claimed_at = NULL WHERE id = ANY(@ids::bigint[]);

-- name: WithdrawUnusableVendorWarnings :execrows
-- Internal: the inactivity sweep. A warning sent to someone whose vendor org
-- or its client is not approved is withdrawn: its days run while the person
-- cannot sign in. They are warned afresh, with full days, once it is usable.
UPDATE memberships m SET inactivity_warned_at = NULL, inactivity_warning_claimed_at = NULL
FROM tenants t
LEFT JOIN tenants c ON c.id = t.client_tenant_id
WHERE t.id = m.tenant_id AND t.kind = 'vendor'
  AND (m.inactivity_warned_at IS NOT NULL OR m.inactivity_warning_claimed_at IS NOT NULL) AND m.disabled_at IS NULL
  AND (t.status <> 'approved' OR c.status IS DISTINCT FROM 'approved');

-- name: DisableInactiveVendorMemberships :execrows
-- Internal: the inactivity sweep. Disables up to @lim vendor org memberships
-- unused since @unused_since whose warning went out before @warned_before,
-- so nobody is disabled without a warning first, nor while their vendor org
-- or its client is not approved.
UPDATE memberships SET disabled_at = now()
WHERE id IN (
  SELECT m.id FROM memberships m
  JOIN tenants t ON t.id = m.tenant_id AND t.kind = 'vendor' AND t.status = 'approved'
  JOIN tenants c ON c.id = t.client_tenant_id AND c.status = 'approved'
  WHERE m.disabled_at IS NULL
    AND m.last_active_at < @unused_since::timestamptz
    AND m.inactivity_warned_at < @warned_before::timestamptz
  ORDER BY m.id
  LIMIT @lim
  FOR UPDATE OF m SKIP LOCKED
);

-- name: SeedMembershipIdle :exec
-- Internal: the seed. Dates a membership's last use, warning and disable, to
-- show the inactivity rules on demo data.
UPDATE memberships
SET last_active_at = @last_active_at::timestamptz,
    inactivity_warned_at = sqlc.narg('warned_at')::timestamptz,
    disabled_at = sqlc.narg('disabled_at')::timestamptz
WHERE tenant_id = @tenant_id AND user_id = @user_id;
