/**
 * Short relative time for dense lists: "just now", "5m ago", "3h ago", "2d ago",
 * "4mo ago", "2y ago". Used where a full date does not fit — the notification
 * dropdown and the segment History rows.
 *
 * Months are 30 days and years are 365, so the two largest units are
 * approximate. They are a label on a list row, not a duration anyone computes
 * with; show the exact date alongside where precision matters (the history rows
 * put one in a `title`).
 *
 * "mo" and not "m" for months — "m" is already minutes.
 *
 * A future date returns "just now", so a few seconds of clock difference
 * between server and browser never shows "-1m ago".
 */
export function timeAgo(dateStr: string): string {
  const ms = new Date(dateStr).getTime()
  if (Number.isNaN(ms)) return 'just now'
  const mins = Math.floor((Date.now() - ms) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  if (days < 30) return `${days}d ago`
  // Switch on days, not on the computed month count: 360 days is 12 months but
  // 0 years, and a `months < 12` branch would fall through to "0y ago".
  if (days < 365) return `${Math.floor(days / 30)}mo ago`
  return `${Math.floor(days / 365)}y ago`
}
