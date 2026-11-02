import { activeIssues, type QaFinding } from './qa-findings'
import type { DisplayStatus } from './segment-status'
import { displayStatusOfRow, type IndexRow } from './editor-index'
import type { TextSearchOptions } from './text-search'

export type QAFilter = 'has_errors' | 'has_warnings' | 'no_issues' | 'ignored'

// Each list is empty = show all; a segment passes a list when it matches any
// value in it, and must pass every list. searchTarget matches the
// translation's active text. No exact match here.
export interface SegmentFilters extends Omit<TextSearchOptions, 'exactMatch'> {
  statuses: string[]
  origins: string[]
  qaFilters: QAFilter[]
  searchText: string    // searches source and/or target text (see scope flags)
  hasComments: boolean  // only segments carrying at least one comment
}

export const DEFAULT_FILTERS: SegmentFilters = {
  statuses: [],
  origins: [],
  qaFilters: [],
  searchText: '',
  searchSource: true,
  searchTarget: true,
  caseSensitive: false,
  useRegex: false,
  hasComments: false,
}

/** Ignored findings do not count as issues: a segment whose only findings are
 *  ignored is "no issues", and matches "ignored". */
function matchesQAFilter(filter: QAFilter, findings: QaFinding[]): boolean {
  switch (filter) {
    case 'has_errors': return activeIssues(findings).some(q => q.severity === 'error')
    case 'has_warnings': return activeIssues(findings).some(q => q.severity === 'warning')
    case 'no_issues': return activeIssues(findings).length === 0
    case 'ignored': return findings.some(q => q.ignored)
  }
}

export function isFilterActive(filters: SegmentFilters): boolean {
  return filters.statuses.length > 0 || filters.origins.length > 0 ||
    filters.qaFilters.length > 0 || filters.searchText.trim() !== '' ||
    filters.hasComments
}

/** How many segments hold each display status — exactly what each status
 *  filter keeps, since both read displayStatusOfRow. */
export function countByDisplayStatus(
  rows: readonly IndexRow[],
  predictedBeforeStep?: ReadonlySet<number>,
): Record<DisplayStatus, number> {
  const counts: Record<DisplayStatus, number> = { untranslated: 0, pre_translated: 0, needs_review: 0, approved: 0, auto_approved: 0 }
  for (const row of rows) counts[displayStatusOfRow(row, predictedBeforeStep)]++
  return counts
}

/**
 * Returns the index positions of the rows that pass all active filters; all of
 * them when no filter is active.
 *
 * The text search runs on the server: `textMatches` holds the segment ids it
 * found (empty while it has not answered), or null when there is no search.
 * `commentCounts` maps translation ID → comment count (only translations that
 * have comments appear).
 */
export function filterSegments(
  rows: readonly IndexRow[],
  qaMap: Map<number, QaFinding[]>,
  filters: SegmentFilters,
  textMatches: ReadonlySet<number> | null,
  commentCounts?: Map<number, number>,
  predictedBeforeStep?: ReadonlySet<number>,
): number[] {
  if (!isFilterActive(filters)) {
    return rows.map((_, i) => i)
  }
  const searching = filters.searchText.trim() !== '' && textMatches !== null
  const result: number[] = []

  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]
    const tid = row.translationId

    // Status filter — uses the derived display status, so the
    // untranslated / pre_translated pills keep working after the enum collapse.
    if (filters.statuses.length > 0) {
      if (!filters.statuses.includes(displayStatusOfRow(row, predictedBeforeStep))) continue
    }

    if (filters.origins.length > 0) {
      if (!filters.origins.includes(row.origin)) continue
    }

    // QA filter
    if (filters.qaFilters.length > 0) {
      const findings = tid != null ? (qaMap.get(tid) ?? []) : []
      if (!filters.qaFilters.some(qa => matchesQAFilter(qa, findings))) continue
    }

    // An untranslated segment has no translation row, so it can't carry a
    // comment either.
    if (filters.hasComments) {
      if (tid == null || !((commentCounts?.get(tid) ?? 0) > 0)) continue
    }

    if (searching && !textMatches.has(row.segmentId)) continue

    result.push(i)
  }

  return result
}
