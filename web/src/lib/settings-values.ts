import type { ProjectSettings } from '../api'
import type { SettingsValues } from '../components/SettingsEditor'
import { workflowStepsFromApi } from './workflow-steps'

// Default TM match penalty, mirroring the settings_profiles column defaults
// (migration 20260826090200). Only used when a payload predates the columns —
// falling back to 0 would silently mean "penalties disabled", which for the
// sublanguage penalty also means "auto-confirm locale variants".
export const DEFAULT_PENALTY_PCT = 1

// Mirrors the settings_profiles.auto_complete column default.
// Falling back to false would read as "auto-complete disabled" for any payload
// that predates the column — the opposite of the shipped default.
export const DEFAULT_AUTO_COMPLETE = true

// Mirrors the settings_profiles.inline_html column default: strings are HTML.
export const DEFAULT_INLINE_HTML = true

// How pre-translation walks several TMs (settings_profiles.tm_lookup_order).
// The column default is match_percent.
export type TMLookupOrder = 'match_percent' | 'tm_order'
export const DEFAULT_TM_LOOKUP_ORDER: TMLookupOrder = 'match_percent'
export const TM_LOOKUP_ORDER_LABELS: Record<TMLookupOrder, string> = {
  match_percent: 'By match percentage',
  tm_order: 'By TM order',
}

// Narrows the API's plain string; anything unknown reads as the default.
export const toTMLookupOrder = (v: string | undefined | null): TMLookupOrder =>
  v === 'tm_order' ? 'tm_order' : DEFAULT_TM_LOOKUP_ORDER

// Maps a GET /projects/{id}/settings payload into the SettingsEditor shape.
// Shared by ProjectSettingsTab (edit mode) and the create-page prefill
// ("start from an existing project"), so the two can't drift.
export function projectSettingsToValues(
  settings: ProjectSettings,
  termBases?: { id: number; name: string }[],
): SettingsValues {
  const project = settings.project
  return {
    fuzzyThreshold: project?.fuzzy_threshold ?? 0.75,
    sublanguagePenaltyPct: project?.sublanguage_penalty_pct ?? DEFAULT_PENALTY_PCT,
    nearExactPenaltyPct: project?.near_exact_penalty_pct ?? DEFAULT_PENALTY_PCT,
    tmLookupOrder: toTMLookupOrder(project?.tm_lookup_order),
    mtOnUpload: project?.mt_on_upload ?? false,
    tmOnUpload: project?.tm_on_upload ?? false,
    autoComplete: project?.auto_complete ?? DEFAULT_AUTO_COMPLETE,
    inlineHTML: project?.inline_html ?? DEFAULT_INLINE_HTML,
    qaProfileId: project?.qa_profile_id ?? null,
    tmProfileId: settings.tm_profile?.id ?? null,
    tmProfileName: settings.tm_profile?.name,
    tmProfileUsageCount: settings.tm_profile?.usage_count,
    tmAssignments: settings.tm_assignments.map(a => ({
      id: a.id,
      tmId: a.tm_id,
      mode: a.mode,
      priority: a.priority,
      penaltyPct: a.penalty_pct ?? 0,
    })),
    mtRouterId: project?.mt_router_id ?? null,
    mtRouterName: project?.mt_router_name ?? undefined,
    termAssignments: settings.term_assignments.map(a => ({
      id: a.id,
      resourceId: a.term_base_id,
      resourceName: termBases?.find(t => t.id === a.term_base_id)?.name ?? `Term base #${a.term_base_id}`,
      sourceLang: a.source_language,
      targetLang: a.target_language,
      mode: a.mode,
      priority: a.priority,
    })),
    workflowSteps: workflowStepsFromApi(settings.workflow_steps),
  }
}
