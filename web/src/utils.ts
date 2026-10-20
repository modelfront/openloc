/** Format an ISO timestamp as a short relative or absolute date. */
export function formatDate(iso: string): string {
  const date = new Date(iso)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60_000)
  const diffHr = Math.floor(diffMs / 3_600_000)
  const diffDays = Math.floor(diffMs / 86_400_000)

  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffHr < 24) return `${diffHr}h ago`
  if (diffDays < 7) return `${diffDays}d ago`

  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: date.getFullYear() !== now.getFullYear() ? 'numeric' : undefined })
}

/**
 * Date and time to the minute in the user's locale and timezone:
 * "Sep 15, 2026, 5:00 PM" or similar. Empty string for a missing or invalid date.
 */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  })
}
