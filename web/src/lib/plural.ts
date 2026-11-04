/** "3 segments" / "1 segment" — the count with its noun, plural unless it is 1. Null renders nothing. */
export function count(n: number | null, noun: string): string {
  if (n === null) return ''
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

/** "1 matches" / "3 match": the count with its verb, for "N match the filters". */
export function matchCount(n: number): string {
  return `${n} ${n === 1 ? 'matches' : 'match'}`
}
