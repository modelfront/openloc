import { clearIdentity } from '@/lib/errtrack';

const BASE = '/api';

/**
 * Typed API error carrying the HTTP status so callers can branch on it
 * (e.g., 409 Conflict from optimistic locking, 403 Forbidden from RBAC).
 */
export class ApiError extends Error {
  status: number;
  /**
   * Full parsed response body (when JSON). Carries route-specific extras like
   * the optimistic-locking conflict payload (`{conflict: {...}}`) so callers
   * don't need a second round-trip to render the banner.
   */
  body?: unknown;
  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.status = status;
    this.body = body;
    this.name = 'ApiError';
  }
}

// Token storage with multiple fallbacks:
// 1. localStorage / sessionStorage (may be blocked by privacy extensions)
// 2. In-memory (last resort for client-side token tracking)
// 3. HttpOnly cookie (set by server, survives all client-side blocks)
//
// The server sets an HttpOnly cookie on login that the browser sends automatically.
// Client-side storage is used only for the `isLoggedIn()` quick check.
let memoryToken: string | null = null;

export function getToken(): string | null {
  try { const v = localStorage.getItem('token'); if (v) return v; } catch { /* storage blocked */ }
  try { const v = sessionStorage.getItem('token'); if (v) return v; } catch { /* storage blocked */ }
  return memoryToken;
}

export function setToken(token: string) {
  memoryToken = token;
  try { localStorage.setItem('token', token); } catch { /* storage blocked */ }
  try { sessionStorage.setItem('token', token); } catch { /* storage blocked */ }
}

function clearToken() {
  memoryToken = null;
  try { localStorage.removeItem('token'); localStorage.removeItem('user'); } catch { /* storage blocked */ }
  try { sessionStorage.removeItem('token'); } catch { /* storage blocked */ }
}

// Quick synchronous check — may return false on refresh when storage is blocked,
// but the HttpOnly cookie may still be valid. Use checkAuth() for a definitive check.
export function isLoggedIn(): boolean {
  return !!getToken();
}

// Definitive auth check: tries the server (HttpOnly cookie will be sent automatically).
// Returns true if the user is authenticated.
export async function checkAuth(): Promise<boolean> {
  if (getToken()) return true;
  try {
    const res = await fetch(`${BASE}/auth/me`, { credentials: 'same-origin' });
    if (res.ok) {
      // HttpOnly cookie is valid — mark as logged in for future sync checks
      memoryToken = '__cookie_auth__';
      return true;
    }
  } catch { /* network error: treated as signed out */ }
  return false;
}

export async function logout() {
  try { await fetch(`${BASE}/auth/logout`, { method: 'POST', credentials: 'same-origin' }); } catch { /* offline: clear the local session anyway */ }
  clearToken();
  // Detach the user/tenant from any error report racing the redirect below.
  // (No import cycle: lib/errtrack deliberately imports nothing from here.)
  clearIdentity();
  // `signedout=1` tells the login page NOT to auto-fire dev-login — otherwise
  // an explicit sign-out in dev would immediately re-authenticate and bounce
  // the user back into the org picker. No effect in prod (dev-login is off).
  window.location.href = '/login?signedout=1';
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const token = getToken();
  // A FormData body sets its own multipart Content-Type, boundary included.
  const headers: Record<string, string> = options?.body instanceof FormData ? {} : { 'Content-Type': 'application/json' };
  if (token && token !== '__cookie_auth__') {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, {
    ...options,
    credentials: 'same-origin', // ensures HttpOnly cookie is sent
    headers: { ...headers, ...(options?.headers as Record<string, string>) },
  });

  if (res.status === 401) {
    logout();
    throw new Error('Session expired');
  }

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: { message: res.statusText } }));
    if (res.status === 403 && err.error?.code === 'demo_reset') await reenterDemo();
    throw new ApiError(res.status, err.error?.message || res.statusText, err);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

const DEMO_RESET_KEY = 'demoResetAt';

/**
 * The demo organization was rebuilt while the visitor had it open:
 * leave the old one, enter the new one, reload the same
 * page. Once per minute at most, so a failing rebuild cannot loop.
 */
async function reenterDemo(): Promise<void> {
  try {
    const last = Number(sessionStorage.getItem(DEMO_RESET_KEY) ?? 0);
    if (Date.now() - last < 60_000) return;
    sessionStorage.setItem(DEMO_RESET_KEY, String(Date.now()));
  } catch {
    return;
  }
  const post = async (path: string) => {
    const t = getToken();
    const res = await fetch(`${BASE}${path}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: t && t !== '__cookie_auth__' ? { Authorization: `Bearer ${t}` } : {},
    });
    if (!res.ok) throw new Error(`${path}: ${res.status}`);
    const body = await res.json();
    setToken(body.data.token);
  };
  try {
    await post('/auth/demo/leave');
    await post('/auth/demo/enter');
    window.location.reload();
  } catch {
    // Left as it is: the banner's "Back to" still works.
  }
}

export { BASE };
