import type { ProjectCreatePayload } from '../api'
import type { SettingsValues } from '../components/SettingsEditor'
import { workflowStepsToPayload } from './workflow-steps'

export interface ProjectCreateForm {
  name: string
  sourceLanguage: string
  targetLanguages: string[]
  customerId: number | null
  /** UTC ISO 8601; null = no deadline. */
  deadline: string | null
  domain: string
  templateId: string
  workflowId: string
}

/**
 * The New Project wizard's form as one request body. Used for the first
 * autosave (POST), every later autosave (PUT /draft) and nothing else — the
 * server sees the identical shape whichever way the form reaches it.
 *
 * With a template the server first applies its assignments, so every list is
 * sent even when empty — an empty list is "replace with nothing", which is
 * what a user who cleared a prefilled list expects. Without a template, empty
 * replaces on a fresh profile are no-ops.
 */
export function buildProjectCreatePayload(form: ProjectCreateForm, s: SettingsValues): ProjectCreatePayload {
  return {
    name: form.name.trim(),
    source_language: form.sourceLanguage,
    target_languages: form.targetLanguages,
    customer_id: form.customerId,
    // The server treats null, omit and "" identically (no deadline).
    deadline: form.deadline,
    // Trimmed here so whitespace never lands as a value; the server
    // trims again.
    domain: form.domain.trim(),
    template_id: form.templateId ? Number(form.templateId) : undefined,
    ...buildProjectSetupPayload(form.workflowId, s),
  }
}

/**
 * The configuration half of the body: settings, the four assignment lists and
 * the workflow. Shared with "Save as template", which sends the same shape
 * under a template's own metadata, so the two can never drift.
 */
export function buildProjectSetupPayload(workflowId: string, s: SettingsValues) {
  return {
    workflow_id: workflowId ? Number(workflowId) : undefined,
    settings: {
      fuzzy_threshold: s.fuzzyThreshold,
      sublanguage_penalty_pct: s.sublanguagePenaltyPct,
      near_exact_penalty_pct: s.nearExactPenaltyPct,
      tm_lookup_order: s.tmLookupOrder,
      mt_on_upload: s.mtOnUpload,
      tm_on_upload: s.tmOnUpload,
      auto_complete: s.autoComplete,
      inline_html: s.inlineHTML,
      qa_profile_id: s.qaProfileId,
      tm_profile_id: s.tmProfileId ?? undefined,
      mt_router_id: s.mtRouterId ?? undefined,
    },
    // A project that follows a TM profile takes its TMs from the profile;
    // sending rows as well is refused by the server.
    tm_assignments: s.tmProfileId != null ? undefined : s.tmAssignments.map(i => ({
      tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0,
    })),
    term_assignments: s.termAssignments.map(i => ({
      term_base_id: i.resourceId, source_language: i.sourceLang, target_language: i.targetLang,
      mode: i.mode ?? 'read', priority: i.priority ?? 0,
    })),
    workflow_steps: s.workflowSteps.length > 0 ? workflowStepsToPayload(s.workflowSteps) : undefined,
  }
}
