import { formatCount } from './duration'

/**
 * Display policy for a TM's language coverage and size, shared by the TM list and the
 * project's TM assignment panel so the same TM summarises identically in both.
 */

/**
 * Target languages shown before the rest collapse into a `+N` chip. Passed as
 * `max` to `<LanguagePair>`. A TM may declare dozens of locales; without a cap
 * one wide TM pushes the customer and entry count off the end of its row.
 */
export const TM_LANGS_SHOWN = 5

/** A TM's size, `1234 → '1,234 entries'`, as the TM list and the TM pickers write it. */
export function tmEntriesLabel(n: number): string {
  return `${formatCount(n)} ${n === 1 ? 'entry' : 'entries'}`
}

/** A term base's size, `1 → '1 concept'`, as the term base list and pickers write it. */
export function conceptsLabel(n: number): string {
  return `${formatCount(n)} ${n === 1 ? 'concept' : 'concepts'}`
}
