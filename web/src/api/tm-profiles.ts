import { request } from './request';
import type { TemplateTMAssignment } from './engines';
import type { TMLookupOrder } from '../lib/settings-values';

/** Names the TM profile a project or template follows. */
export interface TMProfileRef {
  id: number;
  name: string;
  /** Projects + templates following it, this one included; absent on create responses. */
  usage_count?: number;
}

/** One list row: the profile plus the scoring it owns and its usage counts. */
export interface TMProfile {
  id: number;
  tenant_id: number;
  settings_profile_id: number;
  name: string;
  description: string;
  created_by: number | null;
  created_at: string;
  updated_at: string;
  fuzzy_threshold: number;
  sublanguage_penalty_pct: number;
  near_exact_penalty_pct: number;
  tm_lookup_order: string;
  /** TMs bundled in the profile. */
  tm_count: number;
  /** Projects + templates that follow the profile. */
  usage_count: number;
  created_by_name: string;
}

export interface TMProfileDetail extends TMProfile {
  tm_assignments: TemplateTMAssignment[];
}

/** The match scoring a profile owns. Absent fields keep their stored values. */
export interface TMProfileScoring {
  fuzzy_threshold?: number;
  sublanguage_penalty_pct?: number;
  near_exact_penalty_pct?: number;
  tm_lookup_order?: TMLookupOrder;
}

export interface TMProfileTMInput {
  tm_id: number;
  mode: string;
  priority: number;
  penalty_pct?: number;
}

export const listTMProfiles = () =>
  request<{ data: TMProfile[] }>('/tm-profiles').then(r => r.data);

export const getTMProfile = (id: number) =>
  request<{ data: TMProfileDetail }>(`/tm-profiles/${id}`).then(r => r.data);

export const createTMProfile = (data: { name: string; description?: string; tms?: TMProfileTMInput[] } & TMProfileScoring) =>
  request<{ data: TMProfileDetail }>('/tm-profiles', { method: 'POST', body: JSON.stringify(data) }).then(r => r.data);

/** In-place edits from a project/template page send `maxFollowers`: the server
 *  refuses (409 `tm_profile_shared`) once more than that many follow the
 *  profile, so a page that believed it was the only follower cannot change a
 *  setup someone else started relying on meanwhile. */
export interface TMProfileEditOptions {
  maxFollowers?: number;
}
const followersQuery = (opts?: TMProfileEditOptions) =>
  opts?.maxFollowers != null ? `?max_followers=${opts.maxFollowers}` : '';

export const updateTMProfile = (id: number, data: { name?: string; description?: string } & TMProfileScoring, opts?: TMProfileEditOptions) =>
  request<{ data: TMProfileDetail }>(`/tm-profiles/${id}${followersQuery(opts)}`, { method: 'PATCH', body: JSON.stringify(data) }).then(r => r.data);

export const setTMProfileTMs = (id: number, assignments: TMProfileTMInput[], opts?: TMProfileEditOptions) =>
  request<{ data: TemplateTMAssignment[] }>(`/tm-profiles/${id}/tms${followersQuery(opts)}`, {
    method: 'PUT',
    body: JSON.stringify({ assignments }),
  }).then(r => r.data);

/** 409 (code `tm_profile_in_use`) while any project or template follows it. */
export const deleteTMProfile = (id: number) =>
  request<void>(`/tm-profiles/${id}`, { method: 'DELETE' });

// --- Project / template link management. Linking goes through the settings
// PATCH (tm_profile_id); these are the other two directions.

/** Copies the profile's TMs + scoring onto the project's own settings, then unlinks. */
export const detachProjectTMProfile = (projectId: number) =>
  request<{ data: unknown }>(`/projects/${projectId}/tms/detach`, { method: 'POST' }).then(r => r.data);

/** Snapshots the project's current TM scope into a new profile; `link` follows it. */
export const saveProjectTMProfile = (projectId: number, data: { name: string; description?: string; link: boolean }) =>
  request<{ data: { tm_profile: TMProfileRef; linked: boolean } }>(`/projects/${projectId}/save-tm-profile`, {
    method: 'POST',
    body: JSON.stringify(data),
  }).then(r => r.data);

export const detachTemplateTMProfile = (templateId: number) =>
  request<{ data: unknown }>(`/templates/${templateId}/tms/detach`, { method: 'POST' }).then(r => r.data);

export const saveTemplateTMProfile = (templateId: number, data: { name: string; description?: string; link: boolean }) =>
  request<{ data: { tm_profile: TMProfileRef; linked: boolean } }>(`/templates/${templateId}/save-tm-profile`, {
    method: 'POST',
    body: JSON.stringify(data),
  }).then(r => r.data);
