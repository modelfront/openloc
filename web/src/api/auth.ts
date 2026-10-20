import { request, setToken, BASE } from './request';

// An org the current identity can enter. Shape mirrors the backend's
// ListMembershipsWithTenant rows.
export interface Org {
  tenant_id: number;
  tenant_name: string;
  tenant_slug: string;
  role: string;
}

// Result of authenticating (dev-login) OR selecting an org — anything that may
// mint a token. `needs_org_selection` true means no org token was issued yet;
// the client must route to the org picker.
export interface AuthResult {
  token: string;
  needs_org_selection?: boolean;
  orgs?: Org[];
  user: { id: number; email: string; name: string; tenant_id?: number; role?: string };
}

export function saveLoginResult(result: { token: string }) {
  setToken(result.token);
}

// Auth config (which auth methods are available)
export interface AuthConfig {
  google_enabled: boolean;
  microsoft_enabled?: boolean;
  dev_login_enabled: boolean;
  // Email+password login. Always true today; kept as a flag so the
  // login form stays server-driven like the SSO buttons.
  password_enabled?: boolean;
}

/** What an auth call throws: the server's message, plus its error code and HTTP status. */
export type AuthRequestError = Error & { code?: string; status?: number };

// Shared unauthenticated POST for the password-auth endpoints: the
// caller has no token yet, so this goes through fetch (not request()) exactly
// like devLogin, and returns the same AuthResult the SPA already handles.
const authPost = <T = AuthResult>(path: string, body: unknown) =>
  fetch(`${BASE}${path}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(async res => {
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: { message: res.statusText } }));
      const e: AuthRequestError = new Error(err.error?.message || res.statusText);
      e.code = err.error?.code;
      e.status = res.status;
      throw e;
    }
    return res.json() as Promise<T>;
  });

// Email+password login. All failures surface the same message — the
// backend deliberately doesn't reveal whether the account exists.
export const passwordLogin = (email: string, password: string) =>
  authPost('/auth/login', { email, password });

// What the accept page needs to render an invite. 404 = the link is
// invalid/expired/revoked (indistinguishable by design).
export interface InviteLookup {
  email: string;
  org_name: string;
  role: string;
  user_exists: boolean;
  has_password: boolean;
}

export const lookupInvite = (token: string) =>
  fetch(`${BASE}/invites/lookup/${token}`).then(async res => {
    if (!res.ok) throw new Error('invalid_invite');
    const body = await res.json();
    return body.data as InviteLookup;
  });

// Create a NEW password account from an invite. 409 account_exists
// means the email already has an account — sign in normally instead (any
// sign-in materializes the invite).
export const acceptInvite = (token: string, password: string, name: string) =>
  authPost('/auth/accept-invite', { token, password, name });

// Request a password-reset link. Always resolves — the backend never reveals
// whether the account exists.
export const forgotPassword = (email: string) =>
  authPost<{ ok: boolean }>('/auth/forgot-password', { email });

// Redeem a reset link: sets the password and logs in.
export const resetPassword = (token: string, password: string) =>
  authPost('/auth/reset-password', { token, password });

export const getAuthConfig = () =>
  fetch(`${BASE}/auth/config`).then(async res => {
    if (!res.ok) throw new Error('Failed to load auth config');
    return res.json() as Promise<AuthConfig>;
  });

// Dev-only login. With no argument, logs in as the default seed identity
// (the seed owner). Pass an email to log in as any identity (created on
// the fly if it doesn't exist) — handy for exercising multi-org / signup flows
// locally. The backend 404s this in production.
export const devLogin = (email?: string) =>
  fetch(`${BASE}/auth/dev-login`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(email ? { email } : {}),
  }).then(async res => {
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: { message: res.statusText } }));
      throw new Error(err.error?.message || res.statusText);
    }
    return res.json() as Promise<AuthResult>;
  });

// List the orgs the current identity can enter (the workspace picker).
export const listOrgs = () =>
  request<{ data: Org[] }>('/auth/orgs').then(r => r.data);

export interface SelectOrgResponse {
  token: string;
  user: { id: number; email: string; name: string; tenant_id: number; role: string };
  tenant: { id: number; name: string; slug: string };
}

// Enter an org: exchanges the identity token for a tenant-scoped token.
export const selectOrg = (tenantId: number) =>
  request<SelectOrgResponse>(`/auth/orgs/${tenantId}/select`, { method: 'POST' })
    .then(r => { setToken(r.token); return r; });

export interface UserProfile {
  id: number;
  email: string;
  name: string;
  needs_org_selection: boolean;
  // True for platform operators (PLATFORM_ADMIN_EMAILS) — unlocks the
  // pending-signup approvals surface.
  is_platform_admin?: boolean;
  // Present only once an org is selected (tenant-scoped token).
  tenant_id?: number;
  tenant_name?: string;
  tenant_slug?: string;
  role?: string;
  // True when the selected org is not approved. The SPA routes to the
  // blocked screen instead of the app. tenant_status tailors the copy.
  org_pending?: boolean;
  tenant_status?: OrgStatus;
  // True when a platform operator is "logged in as" this org. The SPA
  // hides the operator surface and shows an impersonation banner.
  impersonating?: boolean;
  // True inside the shared, read-only demo organization.
  // The profile is then the demo guide's; demo_return_org_name is the org
  // that leaving the demo returns to.
  demo?: boolean;
  demo_return_org_name?: string;
}

export const getMe = () =>
  request<{ data: UserProfile }>('/auth/me').then(r => r.data);

// Self-serve org signup: create an org from the caller's email
// domain and become its owner (pending operator approval). Stores the returned
// tenant token. On failure the ApiError body carries a `code` the caller can
// branch on ("free_email", "domain_taken").
export interface CreateOrgResponse {
  token: string;
  status: string;
  tenant: { id: number; name: string; slug: string; domain: string };
}

export const createOrg = (name?: string) =>
  request<CreateOrgResponse>('/auth/orgs', {
    method: 'POST',
    body: JSON.stringify(name ? { name } : {}),
  }).then(r => { setToken(r.token); return r; });

// Platform operator surface: every org with its lifecycle status.
export type OrgStatus = 'pending' | 'approved' | 'rejected' | 'suspended';

export interface PlatformOrg {
  tenant_id: number;
  name: string;
  slug: string;
  domain: string | null;
  status: OrgStatus;
  created_at: string;
  owner_email: string | null;
  owner_name: string | null;
  // True when the current operator is a member of this org — the UI hides the
  // Open/Disable actions for it (you can't disable or impersonate your own org).
  is_self: boolean;
}

export const listPlatformOrgs = () =>
  request<{ data: PlatformOrg[] }>('/platform/orgs').then(r => r.data);

// Set an org's status: approve/re-enable ('approved'), decline ('rejected'),
// or disable ('suspended'). 'pending' is not settable.
export const setOrgStatus = (tenantId: number, status: Exclude<OrgStatus, 'pending'>) =>
  request<{ data: { tenant_id: number; status: OrgStatus } }>(`/platform/orgs/${tenantId}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  }).then(r => r.data);

// Operator impersonation: enter an org as its owner to inspect/support
// it. Swaps the current token for a tenant-scoped one on the target org.
// Approved orgs only. Return to your own org via the org switcher.
export const loginAsOrg = (tenantId: number) =>
  request<{ token: string; tenant: { id: number; name: string; slug: string } }>(`/platform/orgs/${tenantId}/login`, {
    method: 'POST',
  }).then(r => { setToken(r.token); return r; });

// The email is not editable: the server refuses any change to it.
export const updateMe = (data: { name: string }) =>
  request<{ data: UserProfile }>('/auth/me', {
    method: 'PATCH',
    body: JSON.stringify(data),
  }).then(r => r.data);

// Dev-only: switch the current user's own role (bypasses admin guards) and
// persist a fresh token carrying the new role. The backend 404s this in
// production. Powers the header RoleSwitcher dev tool.
export const devSetRole = (role: string) =>
  request<{ token: string; role: string }>('/auth/dev-role', {
    method: 'POST',
    body: JSON.stringify({ role }),
  }).then(r => { setToken(r.token); return r; });

// Members
export interface Member {
  id: number;
  email: string;
  name: string;
  role: string;
  created_at: string;
}

export const listMembers = () =>
  request<{ data: Member[] }>('/members').then(r => r.data);

export const updateMemberRole = (userId: number, role: string) =>
  request<{ status: string; role: string }>(`/members/${userId}/role`, {
    method: 'PUT',
    body: JSON.stringify({ role }),
  });

// Invites. The token never reaches the browser: only the invite email carries
// it, so a lost link is fixed with resendInvite.
export interface Invite {
  id: number;
  tenant_id: number;
  email: string;
  role: string;
  invited_by: number;
  invited_by_name?: string;
  accepted_at: string | null;
  expires_at: string;
  created_at: string;
}

export const listInvites = () =>
  request<{ data: Invite[] }>('/invites').then(r => r.data);

export const createInvite = (email: string, role: string) =>
  request<{ data: Invite }>('/invites', {
    method: 'POST',
    body: JSON.stringify({ email, role }),
  }).then(r => r.data);

export const deleteInvite = (id: number) =>
  request<void>(`/invites/${id}`, { method: 'DELETE' });

// Emails the invite again with a new link; the old link stops working.
export const resendInvite = (id: number) =>
  request<{ data: Invite }>(`/invites/${id}/resend`, { method: 'POST' }).then(r => r.data);

// Platform org provisioning: create an approved org and invite its
// owner in one step. The response includes the shareable invite link as a
// fallback if the email doesn't land.
export interface PlatformCreateOrgResponse {
  tenant: { id: number; name: string; slug: string; domain: string | null; status: OrgStatus };
  invite: { email: string; role: string; expires_at: string; link: string };
}

export const platformCreateOrg = (data: { name: string; owner_email: string; domain?: string }) =>
  request<PlatformCreateOrgResponse>('/platform/orgs', {
    method: 'POST',
    body: JSON.stringify(data),
  });

// Accepting an invite: sign in with
// Google/Microsoft via the invite link (`/api/auth/{provider}?invite=TOKEN`),
// or — for a brand-new identity — set a password via acceptInvite() above.
// See AcceptInvite.tsx.

// The shared demo organization. Both store the returned
// token; the caller reloads so every query refetches under it.
export const enterDemo = () =>
  request<{ data: { token: string } }>('/auth/demo/enter', { method: 'POST' })
    .then(r => { setToken(r.data.token); return r.data; });

export const leaveDemo = () =>
  request<{ data: { token: string; needs_org_selection: boolean } }>('/auth/demo/leave', { method: 'POST' })
    .then(r => { setToken(r.data.token); return r.data; });
