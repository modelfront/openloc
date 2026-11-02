/**
 * The order of a Reported issues list: up to three levels, each a column and a
 * direction. The server sorts (the list loads in pages); this module reads the
 * order from the URL, writes it back, and works out what a header click does.
 */

import type { IssueScope } from '@/api'

export type IssueSortKey = 'kind' | 'reporter' | 'area' | 'status' | 'updated' | 'priority' | 'assignee'

export interface IssueSortLevel {
  key: IssueSortKey
  desc: boolean
}

export const MAX_SORT_LEVELS = 3

/** No level: the server's tie-breakers alone, newest report first. */
export const DEFAULT_SORT: IssueSortLevel[] = []

const MEMBER_KEYS: IssueSortKey[] = ['kind', 'reporter', 'area', 'status', 'updated']
const PLATFORM_KEYS: IssueSortKey[] = [...MEMBER_KEYS, 'priority', 'assignee']

/** The keys a list sorts by: priority and assignee are the support team's columns. */
export function sortKeysFor(scope: IssueScope): IssueSortKey[] {
  return scope === 'platform' ? PLATFORM_KEYS : MEMBER_KEYS
}

/**
 * Whether a column's natural order, its first click, is descending: newest
 * first for Updated. The others start ascending: A→Z, or their ranked order
 * (bugs first and worst first, high priority first, needs info first).
 */
export const naturalDesc = (key: IssueSortKey) => key === 'updated'

/**
 * The order in a URL's sort parameter ("status,-updated"). Unknown, repeated
 * and extra keys are dropped rather than sent, so an old or edited link still
 * loads the list.
 */
export function parseSort(param: string | null, keys: IssueSortKey[]): IssueSortLevel[] {
  const levels: IssueSortLevel[] = []
  for (const raw of (param ?? '').split(',')) {
    const desc = raw.startsWith('-')
    const key = (desc ? raw.slice(1) : raw) as IssueSortKey
    if (keys.includes(key) && !levels.some(l => l.key === key) && levels.length < MAX_SORT_LEVELS) {
      levels.push({ key, desc })
    }
  }
  return levels
}

/** The sort parameter for an order; null for the default, which needs none. */
export function formatSort(levels: IssueSortLevel[]): string | null {
  return levels.map(l => (l.desc ? '-' : '') + l.key).join(',') || null
}

/**
 * The order after a click on key's header. Each column runs natural order →
 * reversed → off. A click sorts by that column alone; on the only level it
 * takes the next step, so the third click goes back to the default. Shift+click
 * adds the column as the next level (up to three), or takes a level's next
 * step.
 */
export function nextSort(levels: IssueSortLevel[], key: IssueSortKey, shift: boolean): IssueSortLevel[] {
  const i = levels.findIndex(l => l.key === key)
  if (!shift && !(levels.length === 1 && i === 0)) {
    return [{ key, desc: i === 0 ? levels[0].desc : naturalDesc(key) }]
  }
  if (i < 0) return levels.length < MAX_SORT_LEVELS ? [...levels, { key, desc: naturalDesc(key) }] : levels
  if (levels[i].desc !== naturalDesc(key)) return levels.filter((_, j) => j !== i)
  return levels.map((l, j) => (j === i ? { ...l, desc: !l.desc } : l))
}
