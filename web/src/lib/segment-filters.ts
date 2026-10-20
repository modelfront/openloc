import type { Segment, Translation } from '../api'
import { activeIssues, type QaFinding } from './qa-findings'
import { deriveDisplayStatus, type DisplayStatus } from './segment-status'
import type { TextSearchOptions } from './text-search'

// searchTarget matches the translation's active text. No exact match here.
export interface SegmentFilters extends Omit<TextSearchOptions, 'exactMatch'> {
  statuses: string[]    // empty = show all
  origins: string[]     // empty = show all
  qaFilter: 'all' | 'has_errors' | 'has_warnings' | 'no_issues' | 'ignored'
  searchText: string    // searches source and/or target text (see scope flags)
  hasComments: boolean  // only segments carrying at least one comment
}

export const DEFAULT_FILTERS: SegmentFilters = {
  statuses: [],
  origins: [],
  qaFilter: 'all',
  searchText: '',
  searchSource: true,
  searchTarget: true,
  caseSensitive: false,
  useRegex: false,
  hasComments: false,
}

/**
 * Compiles the text-search predicate once per filterSegments call (the regex /
 * lowercased needle must not be rebuilt per segment). Returns null when no
 * search text is set. An invalid regex or both scopes off matches nothing —
 * the 0/N count badge is the honest signal, not a silently-ignored filter.
 */
function buildTextMatcher(filters: SegmentFilters): ((source: string, target: string) => boolean) | null {
  if (filters.searchText.trim() === '') return null
  if (!filters.searchSource && !filters.searchTarget) return () => false

  let match: (text: string) => boolean
  if (filters.useRegex) {
    // Raw pattern, no trim — whitespace is meaningful in a regex.
    let re: RegExp
    try {
      re = new RegExp(filters.searchText, filters.caseSensitive ? '' : 'i')
    } catch {
      return () => false
    }
    match = text => re.test(text)
  } else if (filters.caseSensitive) {
    const needle = filters.searchText.trim()
    match = text => text.includes(needle)
  } else {
    const needle = filters.searchText.toLowerCase().trim()
    match = text => text.toLowerCase().includes(needle)
  }

  return (source, target) =>
    (filters.searchSource && match(source)) || (filters.searchTarget && match(target))
}

export function isFilterActive(filters: SegmentFilters): boolean {
  return filters.statuses.length > 0 || filters.origins.length > 0 ||
    filters.qaFilter !== 'all' || filters.searchText.trim() !== '' ||
    filters.hasComments
}

/**
 * Returns indices of segments that pass all active filters.
 * If no filters are active, returns all indices.
 *
 * `commentCounts` maps translation ID → comment count (only translations that
 * actually have comments appear — the API omits zero rows). Optional: callers
 * that never enable the hasComments filter can leave it off.
 */
/** How many segments hold each display status — exactly what each status
 *  filter keeps, since both read deriveDisplayStatus. */
export function countByDisplayStatus(
  segments: Segment[],
  translationMap: Map<number, Translation>,
  predictedBeforeStep?: ReadonlySet<number>,
): Record<DisplayStatus, number> {
  const counts: Record<DisplayStatus, number> = { untranslated: 0, pre_translated: 0, needs_review: 0, approved: 0, auto_approved: 0 }
  for (const seg of segments) counts[displayStatusOf(translationMap.get(seg.id), predictedBeforeStep)]++
  return counts
}

/** deriveDisplayStatus with the job's set of translations whose AI verdict
 *  came from an earlier step. */
export function displayStatusOf(t: Translation | undefined, predictedBeforeStep?: ReadonlySet<number>): DisplayStatus {
  return deriveDisplayStatus(t, { predictedBeforeStep: t != null && !!predictedBeforeStep?.has(t.id) })
}

export function filterSegments(
  segments: Segment[],
  translationMap: Map<number, Translation>,
  qaMap: Map<number, QaFinding[]>,
  filters: SegmentFilters,
  commentCounts?: Map<number, number>,
  predictedBeforeStep?: ReadonlySet<number>,
): number[] {
  if (!isFilterActive(filters)) {
    return segments.map((_, i) => i)
  }

  const textMatcher = buildTextMatcher(filters)
  const result: number[] = []

  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i]
    const t = translationMap.get(seg.id)

    // Status filter — uses the derived display status, so the
    // untranslated / pre_translated pills keep working after the enum collapse.
    if (filters.statuses.length > 0) {
      if (!filters.statuses.includes(displayStatusOf(t, predictedBeforeStep))) continue
    }

    // Origin filter
    if (filters.origins.length > 0) {
      const origin = (t?.origin?.valid ? t.origin.translation_origin : '') ?? ''
      if (!filters.origins.includes(origin)) continue
    }

    // QA filter. Ignored findings do not count as issues: a segment whose only
    // findings are ignored is "no issues", and shows up under "ignored".
    if (filters.qaFilter !== 'all') {
      const all = t ? (qaMap.get(t.id) ?? []) : []
      const issues = activeIssues(all)
      if (filters.qaFilter === 'has_errors') {
        if (!issues.some(q => q.severity === 'error')) continue
      } else if (filters.qaFilter === 'has_warnings') {
        if (!issues.some(q => q.severity === 'error' || q.severity === 'warning')) continue
      } else if (filters.qaFilter === 'no_issues') {
        if (issues.length > 0) continue
      } else if (filters.qaFilter === 'ignored') {
        if (!all.some(q => q.ignored)) continue
      }
    }

    // Comment filter — an untranslated segment has no translation row, so it
    // can't carry a comment either; those drop out with the `!t` guard.
    if (filters.hasComments) {
      if (!t || !((commentCounts?.get(t.id) ?? 0) > 0)) continue
    }

    // Text search
    if (textMatcher && !textMatcher(seg.source_text, t?.active_text ?? '')) continue

    result.push(i)
  }

  return result
}
