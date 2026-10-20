import type { QaProfile, QaRule, WorkflowStep } from '../types/generated';
import type { AIModelParam, ModelSettings } from './ai';
import type { UnusableReason } from './ai-engines';
import type { ReportDTO } from '../types/qa';
import type { QaFinding } from '../lib/qa-findings';
import { request } from './request';
import { listTemplateWorkflowSteps } from './workflows';
import type { TMLookupOrder } from '../lib/settings-values';

// MT engines
export interface MTEngine {
  id: number;
  name: string;
  engine_type: string;
  /**
   * The stored key is NEVER returned — only whether one is set, so no MT
   * credential reaches a browser in plaintext.
   */
  has_api_key: boolean;
  /**
   * Where the engine came from: '' = built here, 'deployment' = provisioned
   * with the org from what the host supplies. PROVENANCE ONLY — it restricts
   * nothing, and a deployment engine is editable and deletable like any other.
   */
  provisioned_by: '' | 'deployment';
  /** The AI settings key this engine spends; null for a deployment engine. */
  token_id: number | null;
  /** Provider request options, keyed by MTEngineParam.key. Never null. */
  settings: MTEngineSettings;
  /** Why it cannot translate, or '' when it can. */
  unusable: UnusableReason;
  api_url: string | null;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

/**
 * An engine's provider options. Deliberately the SAME type the LLM model
 * settings use — both are "a ParamDef-keyed bag of values", and the one
 * settings panel renders both.
 */
export type MTEngineSettings = ModelSettings;

/**
 * One engine type a user may build, with the options its provider accepts.
 * `params` is the same ParamDef shape the LLM model editor renders, so both
 * settings panels are the one component.
 */
export interface MTEngineCatalogEntry {
  engine_type: string;
  name: string;
  needs_token: boolean;
  params: AIModelParam[];
}

export const listMTEngineCatalog = () =>
  request<{ data: MTEngineCatalogEntry[] }>('/mt-engines/catalog').then(r => r.data);

export const listMTEngines = () =>
  request<{ data: MTEngine[] }>('/mt-engines').then(r => r.data);

export interface MTEngineInput {
  name: string;
  engine_type: string;
  token_id?: number | null;
  settings?: MTEngineSettings;
  enabled?: boolean;
}
// No `api_url`: the endpoint follows the engine type, read from the deployment
// on the server. A request cannot carry one, on create or update.

export const createMTEngine = (data: MTEngineInput) =>
  request<{ data: MTEngine }>('/mt-engines', { method: 'POST', body: JSON.stringify(data) }).then(r => r.data);

/**
 * Every field optional: the backend treats an absent field as "keep current".
 * A deployment engine takes a rename and a switch like any other; its TYPE is
 * still the deployment's, so retyping one to something this deployment does not
 * supply is refused.
 *
 * `settings` is a REPLACEMENT, not a merge: clearing an option means sending
 * the object without it.
 */
export const updateMTEngine = (id: number, data: Partial<MTEngineInput> & { enabled?: boolean }) =>
  request<{ data: MTEngine }>(`/mt-engines/${id}`, { method: 'PATCH', body: JSON.stringify(data) }).then(r => r.data);

export const deleteMTEngine = (id: number) =>
  request<void>(`/mt-engines/${id}`, { method: 'DELETE' });

export interface EngineLanguage {
  code: string;
  name: string;
}

/** What an engine says it translates: the languages it reads and the ones it
 *  writes. The two lists differ — DeepL reads EN and writes EN-GB and EN-US.
 *  `per_source` is present only for an engine whose targets differ per source. */
export interface EngineCatalog {
  sources: EngineLanguage[];
  targets: EngineLanguage[];
  per_source?: Record<string, string[]>;
}

export const getEngineLanguages = (id: number) =>
  request<{ data: EngineCatalog }>(`/mt-engines/${id}/languages`).then(r => r.data);

// Templates
export interface Template {
  id: number;
  name: string;
  mt_on_upload: boolean;
  tm_on_upload: boolean;
  fuzzy_threshold: number;
  sublanguage_penalty_pct?: number;
  near_exact_penalty_pct?: number;
  tm_lookup_order?: string;
  // Optional so a payload predating the field falls back to
  // the column default (on) rather than reading as disabled.
  auto_complete?: boolean;
  // Optional like auto_complete: an older payload reads as the default (on).
  inline_html?: boolean;
  qa_profile_id?: number | null;
  // Library workflow the template's steps were copied from.
  workflow_id?: number | null;
  workflow_name?: string | null;
  // The MT router the template's settings profile points at (null = no MT).
  mt_router_id?: number | null;
  mt_router_name?: string | null;
  // Project name, languages, customer and domain a project made from the template starts with.
  project_name?: string | null;
  source_language?: string | null;
  target_languages?: string[];
  customer_id?: number | null;
  domain?: string | null;
  created_at: string;
  updated_at: string;
}

// Per-TM — languages come from the TM's own metadata.
export interface TemplateTMAssignment {
  id: number;
  settings_profile_id: number;
  tm_id: number;
  mode: string;
  priority: number;
  penalty_pct?: number;
}

export interface TemplateTermAssignment {
  id: number;
  template_id: number;
  term_base_id: number;
  source_language: string;
  target_language: string;
  mode: string;
  priority: number;
}

export interface TemplateDetail {
  template: Template;
  tm_assignments: TemplateTMAssignment[];
  // The TM profile the template follows, or null (see ProjectSettings.tm_profile).
  tm_profile?: import('./tm-profiles').TMProfileRef | null;
  term_assignments: TemplateTermAssignment[];
  workflow_steps?: WorkflowStep[];
}

export const listTemplates = () =>
  request<{ data: Template[] }>('/templates').then(r => r.data);

export interface TemplatesPage {
  data: Template[];
  cursor: number;
  has_more: boolean;
}

// One page of templates, newest first, optionally filtered by name. Returns the
// FULL page object so useInfiniteQuery can drive cursor pagination.
// `cursor === 0` means the first page; the response's `cursor` is the keyset
// for the next one.
export const listTemplatesPage = (q: string, cursor = 0, limit = 20) => {
  const qs = new URLSearchParams({ cursor: String(cursor), limit: String(limit) });
  if (q) qs.set('q', q);
  return request<TemplatesPage>(`/templates?${qs.toString()}`);
};

/** Fetch template detail with all assignments. Also fetches workflow steps and merges them in. */
export const getTemplate = async (id: number): Promise<TemplateDetail> => {
  const detail = await request<{ data: TemplateDetail }>(`/templates/${id}`).then(r => r.data);
  try {
    const steps = await listTemplateWorkflowSteps(id);
    detail.workflow_steps = steps;
  } catch {
    detail.workflow_steps = [];
  }
  return detail;
};

// The optional setup a create request carries — the same shape as
// createProject's, so the New project form saves as either.
export interface SetupPayload {
  settings?: {
    fuzzy_threshold?: number; mt_on_upload?: boolean; tm_on_upload?: boolean; auto_complete?: boolean; inline_html?: boolean;
    sublanguage_penalty_pct?: number; near_exact_penalty_pct?: number; tm_lookup_order?: TMLookupOrder;
    qa_profile_id?: number | null; tm_profile_id?: number; mt_router_id?: number;
  };
  tm_assignments?: { tm_id: number; mode: string; priority: number; penalty_pct?: number }[];
  term_assignments?: { term_base_id: number; source_language: string; target_language: string; mode: string; priority: number }[];
  workflow_id?: number;
  workflow_steps?: unknown[];
}

export interface CreateTemplatePayload extends SetupPayload {
  name: string;
  mt_on_upload?: boolean; tm_on_upload?: boolean; fuzzy_threshold?: number;
  project_name?: string; source_language?: string; target_languages?: string[];
  customer_id?: number | null; domain?: string;
}

export const createTemplate = (data: CreateTemplatePayload) =>
  request<{ data: Template }>('/templates', { method: 'POST', body: JSON.stringify(data) }).then(r => r.data);

export const updateTemplate = (id: number, data: { name: string; mt_on_upload: boolean; tm_on_upload: boolean; fuzzy_threshold?: number; sublanguage_penalty_pct?: number; near_exact_penalty_pct?: number; tm_lookup_order?: TMLookupOrder; auto_complete?: boolean; inline_html?: boolean; qa_profile_id?: number | null; tm_profile_id?: number; mt_router_id?: number | null; project_name?: string; source_language?: string; target_languages?: string[]; customer_id?: number | null; domain?: string }) =>
  request<{ data: Template }>(`/templates/${id}`, { method: 'PATCH', body: JSON.stringify(data) }).then(r => r.data);

export const deleteTemplate = (id: number) =>
  request<void>(`/templates/${id}`, { method: 'DELETE' });

// Deep copy: "Copy of {name}" with the same scalars (incl. QA profile) and
// all TM/MT/term/workflow assignments on a fresh settings profile.
export const duplicateTemplate = (id: number) =>
  request<{ data: Template }>(`/templates/${id}/duplicate`, { method: 'POST' }).then(r => r.data);

export const setTemplateTMAssignments = (id: number, assignments: { tm_id: number; mode: string; priority: number }[]) =>
  request<{ data: TemplateTMAssignment[] }>(`/templates/${id}/tms`, {
    method: 'PUT',
    body: JSON.stringify({ assignments }),
  }).then(r => r.data);

export const setTemplateTermAssignments = (id: number, assignments: { term_base_id: number; source_language: string; target_language: string; mode: string; priority: number }[]) =>
  request<{ data: TemplateTermAssignment[] }>(`/templates/${id}/terminology`, {
    method: 'PUT',
    body: JSON.stringify({ assignments }),
  }).then(r => r.data);

// Languages (canonical TMS language list)
export interface Language {
  code: string;
  name: string;
}

export const listLanguages = () =>
  request<{ data: Language[] }>('/languages').then(r => r.data);

// ─── QA profiles ───

export interface QAProfileWithRules {
  profile: QaProfile;
  rules: QaRule[];
}

export const listQAProfiles = () =>
  request<{ data: QaProfile[] }>('/qa-profiles').then(r => r.data);

export const createQAProfile = (name: string, description: string) =>
  request<{ data: QaProfile }>('/qa-profiles', {
    method: 'POST',
    body: JSON.stringify({ name, description }),
  }).then(r => r.data);

export const getQAProfile = (id: number) =>
  request<{ data: QAProfileWithRules }>(`/qa-profiles/${id}`).then(r => r.data);

export const updateQAProfile = (id: number, name: string, description: string) =>
  request<{ data: QaProfile }>(`/qa-profiles/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name, description }),
  }).then(r => r.data);

export const deleteQAProfile = (id: number) =>
  request(`/qa-profiles/${id}`, { method: 'DELETE' });

export const upsertQARule = (profileId: number, checkType: string, enabled: boolean, severity: string, config?: Record<string, unknown>) =>
  request<{ data: QaRule }>(`/qa-profiles/${profileId}/rules`, {
    method: 'POST',
    body: JSON.stringify({ check_type: checkType, enabled, severity, ...(config ? { config } : {}) }),
  }).then(r => r.data);

// ─── QA Checks ───

export interface QARunResult {
  issues: number;
  message: string;
}

// Job-wide report. `summary.ignored` counts findings a human marked as
// ignored; those rows stay in `results` with `ignored: true`.
export type QAReport = ReportDTO;

/** Runs QA on the whole job, or on the given translations (at most SEGMENT_BULK_MAX). */
export const runQA = (jobId: number, translationIds?: number[]) =>
  request<{ data: QARunResult }>(`/jobs/${jobId}/run-qa`, {
    method: 'POST',
    ...(translationIds ? { body: JSON.stringify({ translation_ids: translationIds }) } : {}),
  }).then(r => r.data);

export const getQAReport = (jobId: number) =>
  request<{ data: QAReport }>(`/jobs/${jobId}/qa-report`).then(r => r.data);

export const getTranslationQA = (translationId: number) =>
  request<{ data: QaFinding[] }>(`/translations/${translationId}/qa`).then(r => r.data);

// runTranslationQA re-computes AND persists QA for a single segment, returning
// the fresh results (same shape as getTranslationQA). Used by the CAT editor
// after autosave/confirm so the edited segment's badges stay current without a
// manual full-job Run QA.
export const runTranslationQA = (translationId: number) =>
  request<{ data: QaFinding[] }>(`/translations/${translationId}/qa`, { method: 'POST' }).then(r => r.data);

// Ignore / un-ignore one finding on one segment. The flag flips on the stored
// row; both calls return the segment's full finding list afterwards so callers
// merge it exactly like a runTranslationQA response.
export const ignoreQAFinding = (translationId: number, checkType: string) =>
  request<{ data: QaFinding[] }>(`/translations/${translationId}/qa/${encodeURIComponent(checkType)}/ignore`, {
    method: 'POST',
  }).then(r => r.data);

export const unignoreQAFinding = (translationId: number, checkType: string) =>
  request<{ data: QaFinding[] }>(`/translations/${translationId}/qa/${encodeURIComponent(checkType)}/ignore`, {
    method: 'DELETE',
  }).then(r => r.data);
