/**
 * The entry count above the TM browser's table. A search counts its matches
 * on the first page only, and may leave the total out when counting ran too
 * long; then the label shows what is loaded, with "+" while more pages wait.
 */
export function tmEntryCountLabel(loaded: number, total: number | undefined, searching: boolean, hasMore: boolean): string {
  const noun = searching ? 'matching entries' : 'entries'
  if (total === undefined) return `${loaded.toLocaleString()}${hasMore ? '+' : ''} ${noun}`
  if (total === 0) return `0 ${noun}`
  return `${loaded.toLocaleString()} of ${total.toLocaleString()} ${noun}`
}
