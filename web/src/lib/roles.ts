// Shared role helpers for the four-role model.
//
// Roles in the system:
//   - owner            : full super-admin (everything admin can + owner-only
//                        deletes: customer, vendor)
//   - admin            : full operational power, minus owner-only deletes
//   - project_manager  : admin minus delete-project/TM/TB; own tasks only
//   - linguist         : restricted — Tasks + the CAT editor for jobs assigned to
//                        them.
// A vendor is its own org (see isVendorOrg), not a role.
//
// Keep these in sync with the backend (internal/auth/roles.go + api.validRoles).

/** Human labels for the four roles. Never render a raw role value in the UI. */
export const ROLE_LABELS: Record<string, string> = {
  owner: 'Owner',
  admin: 'Admin',
  project_manager: 'Project manager',
  linguist: 'Linguist',
}

/** The four roles, most powerful first. */
export const ROLE_ORDER = ['owner', 'admin', 'project_manager', 'linguist'] as const

/** Polished label for a role value; falls back to the raw value if unknown. */
export function roleLabel(role: string | undefined | null): string {
  return (role && ROLE_LABELS[role]) || role || ''
}

/** owner or admin — the full-access tier (owner-only deletes gated separately). */
export function isAdminRole(role: string | undefined | null): boolean {
  return role === 'owner' || role === 'admin'
}

/**
 * owner, admin, or project_manager — the roles that may manage projects, TMs,
 * terminology, customers, vendors, templates, engines, and QA profiles.
 */
export function isManagerRole(role: string | undefined | null): boolean {
  return role === 'owner' || role === 'admin' || role === 'project_manager'
}

/**
 * The restricted linguist role: sees only the Tasks tab + the CAT editor for
 * jobs assigned to them. Everything else is hidden (nav) and blocked (routes +
 * backend). See internal/auth/roles.go for the server-side enforcement.
 */
export function isLinguistRole(role: string | undefined | null): boolean {
  return role === 'linguist'
}

/**
 * A vendor org: its own org, linked to one client org. Its admins manage
 * members, its project managers see Projects and Tasks, its linguists see
 * Tasks only; nobody creates projects or resources. The server enforces the
 * same limits; this only shapes the UI.
 */
export function isVendorOrg(me: { tenant_kind?: string } | undefined | null): boolean {
  return me?.tenant_kind === 'vendor'
}

/** The roles a member can be given. A vendor org has no owner. */
export function memberRoles(vendorOrg: boolean): string[] {
  return ROLE_ORDER.filter(r => !vendorOrg || r !== 'owner')
}

/**
 * The roles Invite member offers. In a vendor org an admin is made by
 * promoting a member, so invites bring project managers and linguists.
 */
export function inviteRoles(vendorOrg: boolean): [string, ...string[]] {
  return vendorOrg ? ['project_manager', 'linguist'] : ['admin', 'project_manager', 'linguist']
}
