import type { QueryClient, QueryKey } from '@tanstack/react-query'
import type { ResultDTO, ReportDTO, ReportSummary } from '../types/qa'
import { count } from './plural'
import { chipDotClass, chipTextClass, type ChipColor } from '@/components/ui/chip'

/** One stored QA finding, as returned by every QA endpoint (types/qa.ts). */
export type QaFinding = ResultDTO

export const EMPTY_QA_SUMMARY: ReportSummary = { error: 0, warning: 0, info: 0, ignored: 0 }

/** Findings that still need attention — ignored ones are excluded. */
export function activeIssues<T extends { ignored?: boolean }>(issues: T[]): T[] {
  return issues.filter(i => !i.ignored)
}

/**
 * Counts findings per bucket the same way the backend does: an ignored
 * finding leaves its severity bucket and lands in `ignored`.
 */
export function countQASummary(results: QaFinding[]): ReportSummary {
  const summary: ReportSummary = { ...EMPTY_QA_SUMMARY }
  for (const r of results) {
    if (r.ignored) summary.ignored++
    else if (r.severity === 'error') summary.error++
    else if (r.severity === 'warning') summary.warning++
    else if (r.severity === 'info') summary.info++
  }
  return summary
}

/**
 * Toolbar label. "QA (N)" while there are open errors + warnings, otherwise
 * "Run QA" — a clean job and a job that was never checked read the same, and
 * the button does the same thing either way (a full re-run). Ignored findings
 * are not counted and not shown here.
 */
export function qaButtonLabel(isPending: boolean, summary: ReportSummary | null | undefined): string {
  if (isPending) return 'Checking...'
  const open = summary ? summary.error + summary.warning : 0
  return open > 0 ? `QA (${open})` : 'Run QA'
}

/** Toast after a job-wide run. */
export function qaRunToast(summary: ReportSummary): string {
  const total = summary.error + summary.warning
  const base = `QA completed: ${total} issue(s) found`
  return summary.ignored > 0 ? `${base}, ${summary.ignored} ignored` : base
}

/** Toast after a run on a segment selection: open findings on those rows. */
export function qaSelectionToast(results: readonly { translation_id: number; severity: string; ignored: boolean }[], ids: readonly number[]): string {
  const picked = new Set(ids)
  const open = results.filter(r => picked.has(r.translation_id) && !r.ignored && (r.severity === 'error' || r.severity === 'warning')).length
  return `QA on ${count(ids.length, 'segment')}: ${open} issue(s) found`
}

/**
 * Structural equality for a row's finding list, used by the SegmentRow memo
 * comparator. Length alone misses an ignore flip (same rows, one flag changed),
 * which is exactly the change the dot has to re-render for.
 */
export function qaIssuesEqual(a: QaFinding[], b: QaFinding[]): boolean {
  if (a === b) return true
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    const x = a[i], y = b[i]
    if (x.id !== y.id || x.check_type !== y.check_type || x.message !== y.message ||
        x.severity !== y.severity || !!x.ignored !== !!y.ignored) return false
  }
  return true
}

/** Display labels for the three severities (never render the raw enum). */
export const SEVERITY_LABELS: Record<string, string> = { error: 'Error', warning: 'Warning', info: 'Info' }

export function severityLabel(severity: string): string {
  return SEVERITY_LABELS[severity] ?? severity
}

/** The three severities, most severe first. */
export const SEVERITIES = ['error', 'warning', 'info'] as const
export type Severity = (typeof SEVERITIES)[number]

export const SEVERITY_CHIP_COLORS: Record<Severity, ChipColor> = {
  error: 'red',
  warning: 'yellow',
  info: 'blue',
}

/** Chip colour for a severity; an unknown value reads as info. */
export function severityColor(severity: string): ChipColor {
  return SEVERITY_CHIP_COLORS[severity as Severity] ?? SEVERITY_CHIP_COLORS.info
}

/** Group a report's rows by translation id for per-row lookup in the editor. */
export function groupFindingsByTranslation(results: QaFinding[] | undefined): Map<number, QaFinding[]> {
  const map = new Map<number, QaFinding[]>()
  for (const r of results ?? []) {
    const arr = map.get(r.translation_id) ?? []
    arr.push(r)
    map.set(r.translation_id, arr)
  }
  return map
}

/** Background for an active finding's dot. */
export function severityDotClass(severity: string): string {
  return chipDotClass(severityColor(severity))
}

/** An ignored finding's dot: a hollow grey ring instead of a severity fill. */
export const IGNORED_DOT_CLASS = 'border-2 border-gray-400 dark:border-gray-500 bg-transparent'

/** Dot classes for one finding: severity fill, or the ring when ignored. */
export function findingDotClass(issue: { severity: string; ignored?: boolean }): string {
  return issue.ignored ? IGNORED_DOT_CLASS : severityDotClass(issue.severity)
}

const SEVERITY_RANK: Record<string, number> = { error: 3, warning: 2, info: 1 }

/**
 * The most severe active finding's severity: error > warning > info. Ignored
 * findings don't count; null when every finding is ignored (or there are none).
 */
export function worstSeverity(issues: { severity: string; ignored?: boolean }[]): string | null {
  let worst: string | null = null
  for (const f of activeIssues(issues)) {
    if (worst === null || (SEVERITY_RANK[f.severity] ?? 0) > (SEVERITY_RANK[worst] ?? 0)) worst = f.severity
  }
  return worst
}

/** Text colour for the severity word in the popover. */
export function severityTextClass(severity: string): string {
  return chipTextClass(severityColor(severity))
}

/** Replace one segment's rows in a report and recount, like the backend does. */
export function mergeQAReportRows(prev: ReportDTO | undefined, translationId: number, issues: QaFinding[]): ReportDTO {
  // Seed an empty report when the initial load has not landed yet: autosave
  // is a first-class QA trigger and must populate dots on its own.
  const base = prev ?? { summary: EMPTY_QA_SUMMARY, results: [] }
  const results = [...base.results.filter(r => r.translation_id !== translationId), ...issues]
  return { summary: countQASummary(results), results }
}

/**
 * Patch the cached job report with one segment's fresh findings.
 *
 * Cancels an in-flight refetch first. A confirm both invalidates the report
 * (via invalidateJobContext) and re-runs this segment's QA; the server-side
 * confirm gate only computes, it never persists, so a GET racing ahead reads
 * the pre-confirm rows. Left alone, that GET resolving after this patch would
 * overwrite the fresh rows with stale ones. Cancelling makes the patch the
 * last writer; the next confirm or run refetches anyway.
 */
export async function patchQAReport(
  queryClient: QueryClient,
  key: QueryKey,
  translationId: number,
  issues: QaFinding[],
): Promise<void> {
  await queryClient.cancelQueries({ queryKey: key })
  queryClient.setQueryData<ReportDTO>(key, prev => mergeQAReportRows(prev, translationId, issues))
}
