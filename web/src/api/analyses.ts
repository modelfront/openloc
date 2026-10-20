// Saved analyses API client. Mirrors the
// shape of the generated Go types but exposes a friendlier camelCase
// surface where the UI calls into it. The full 48-column job_analysis row
// is preserved as-is — the detail view groups columns by band at render
// time.
import { request } from './request'
import { downloadFrom } from './download'
import type { Analysis, JobAnalysis } from '../types/generated'

export type { Analysis, JobAnalysis }

export interface CreateAnalysisInput {
  name?: string
  job_ids: number[]
}

export interface AnalysisDetail {
  analysis: Analysis
  breakdowns: JobAnalysis[]
}

// POST /projects/{id}/analyses — creates a row in 'pending' and dispatches
// the worker. Returns the created row immediately; the UI polls / waits on
// SSE for the transition to 'completed'.
export const createAnalysis = (projectId: number, input: CreateAnalysisInput) =>
  request<{ data: Analysis }>(`/projects/${projectId}/analyses`, {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data)

// GET /projects/{id}/analyses — list view, most recent first.
export const listAnalyses = (projectId: number) =>
  request<{ data: Analysis[] }>(`/projects/${projectId}/analyses`).then(r => r.data)

// GET /analyses/{id} — detail view with breakdowns. The breakdowns array
// has one row per (job, target_language) for the analysis.
export const getAnalysis = (analysisId: number) =>
  request<{ data: AnalysisDetail }>(`/analyses/${analysisId}`).then(r => r.data)

// DELETE /analyses/{id} — hard delete (per the planning doc's Resolved
// Decisions). CASCADE sweeps the breakdown rows.
export const deleteAnalysis = (analysisId: number) =>
  request<void>(`/analyses/${analysisId}`, { method: 'DELETE' })

export type AnalysisExportFormat = 'csv' | 'xlsx'

// GET /analyses/{id}/export — a completed analysis as a file. The server
// names it "<analysis name> <yyyy-mm-dd>.<format>".
export const exportAnalysis = (analysisId: number, format: AnalysisExportFormat) =>
  downloadFrom(`/analyses/${analysisId}/export?format=${format}`, `analysis.${format}`)

// JobLeverage — live per-job × target-language breakdown for the
// CAT-editor status bar. Same 6 bands as a saved analysis, computed from
// stored translations rather than persisted. No auto_approved subset
// split into a separate object — flat fields so the bar can render with
// minimal indirection.
export interface LeverageBand {
  segments: number
  words: number
  characters: number
  segments_auto_approved: number
  words_auto_approved: number
  characters_auto_approved: number
  // Needs-review subset (status='needs_review'). Mutually exclusive with
  // the auto_approved subset since `status` is a single enum value.
  segments_needs_review: number
  words_needs_review: number
  characters_needs_review: number
}

export interface JobLeverage {
  job_id: number
  target_language: string
  tm_101:      LeverageBand
  tm_100:      LeverageBand
  tm_85_99:    LeverageBand
  tm_65_84:    LeverageBand
  new:         LeverageBand
  repetitions: LeverageBand
}

export const getJobLeverage = (jobId: number) =>
  request<{ data: JobLeverage }>(`/jobs/${jobId}/leverage`).then(r => r.data)

// Band IDs in the order they should render. Reversed from match-percent
// descending → ascending so repetitions/new (the "work to do" side) sit on
// the left and high-leverage matches drift right. Keeps every consumer
// (list, detail, CSV) sorted the same way. tm_101 (101% in-context exact)
// is populated by the pipeline; repetitions stays reserved (zero).
// Surfaced as "101%", never "ICE".
export const ANALYSIS_BANDS = [
  { id: 'repetitions', label: 'Repetitions' },
  { id: 'new', label: 'New' },
  { id: 'tm_65_84', label: '65–84%' },
  { id: 'tm_85_99', label: '85–99%' },
  { id: 'tm_100', label: '100%' },
  { id: 'tm_101', label: '101%' },
] as const

export type AnalysisBandId = (typeof ANALYSIS_BANDS)[number]['id']

// Helper: sum a band's totals across all breakdown rows + a target language
// filter. Returns { segments, words, characters, weighted_words } and the
// matching auto_approved subset. Pure — drives both the per-job and
// project-rollup views from the same job_analysis rows.
export function sumBand(
  rows: JobAnalysis[],
  band: AnalysisBandId,
  targetLanguage?: string,
): { segments: number; words: number; characters: number; weighted_words: number; auto_approved: { segments: number; words: number; characters: number; weighted_words: number } } {
  const filter = targetLanguage ? rows.filter(r => r.target_language === targetLanguage) : rows
  const out = {
    segments: 0,
    words: 0,
    characters: 0,
    weighted_words: 0,
    auto_approved: { segments: 0, words: 0, characters: 0, weighted_words: 0 },
  }
  for (const r of filter) {
    const row = r as unknown as Record<string, number>
    out.segments += row[`${band}_segments`] ?? 0
    out.words += row[`${band}_words`] ?? 0
    out.characters += row[`${band}_characters`] ?? 0
    out.weighted_words += row[`${band}_weighted_words`] ?? 0
    out.auto_approved.segments += row[`${band}_segments_auto_approved`] ?? 0
    out.auto_approved.words += row[`${band}_words_auto_approved`] ?? 0
    out.auto_approved.characters += row[`${band}_characters_auto_approved`] ?? 0
    out.auto_approved.weighted_words += row[`${band}_weighted_words_auto_approved`] ?? 0
  }
  return out
}
