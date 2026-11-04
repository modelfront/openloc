/** "Nov 3": a day and a month in the user's locale, for a summary line or a dense cell. */
export function dayMonth(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}
