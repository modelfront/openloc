import { request } from './request';

// ─── Personal access tokens ───
//
// The credential MCP clients (Claude Code / Claude Desktop) and headless agents
// present instead of the browser's session JWT.
//
// A token is scoped to ONE (user, org) pair: a user who belongs to three orgs
// creates three tokens. That's deliberate — it keeps the org out of every MCP
// tool argument, so no tool can be talked into crossing an org boundary.
//
// The plaintext secret is returned exactly once, by createAPIToken. Every other
// response carries only the display `prefix`.

export type TokenCapability = 'read' | 'write';

export interface APIToken {
  id: number;
  name: string;
  /** Display fragment, e.g. "eltm_pat_a1b2c3". Never the full secret. */
  prefix: string;
  capability: TokenCapability;
  last_used_at: string | null;
  expires_at: string;
  revoked_at: string | null;
  created_at: string;
  /** Only present on the manager-wide listing (`all: true`). */
  user_email?: string;
  user_name?: string;
}

export interface CreatedAPIToken {
  /** The plaintext token. This is the ONLY time it is ever available. */
  token: string;
  api_token: APIToken;
}

/**
 * Lists the caller's own tokens. Managers may pass `all` to see every token in
 * the org, which is what makes revoking a departing teammate's agent access
 * possible.
 */
export const listAPITokens = (all = false) =>
  request<{ data: APIToken[] }>(`/api-tokens${all ? '?all=true' : ''}`).then(r => r.data);

export const createAPIToken = (params: {
  name: string;
  expires_in_days?: number;
  capability?: TokenCapability;
}) =>
  request<{ data: CreatedAPIToken }>('/api-tokens', {
    method: 'POST',
    body: JSON.stringify(params),
  }).then(r => r.data);

export const revokeAPIToken = (id: number) =>
  request<void>(`/api-tokens/${id}`, { method: 'DELETE' });
