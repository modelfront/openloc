/**
 * The "who and when" a TM match card shows for its entry: the last change on
 * the card, the creation and the last change in its tooltip.
 */

import type { TMMatch } from '../api'
import { formatDateTime } from '../utils'

/** "Oct 12, 2026, 2:32 PM · Anna Miller", or the date alone when no user was
 *  recorded. */
export function tmStampText(at: string, by: string): string {
  const when = formatDateTime(at)
  return by ? `${when} · ${by}` : when
}

export interface TMEntryStamps {
  changed: string
  created: string
}

export function tmEntryStamps(m: Pick<TMMatch, 'created_at' | 'updated_at' | 'created_by' | 'updated_by'>): TMEntryStamps {
  return {
    changed: tmStampText(m.updated_at, m.updated_by),
    created: tmStampText(m.created_at, m.created_by),
  }
}
