// Time-of-day text for TimeInput: parsing what a user types, and matching it
// against the dropdown's "HH:mm" slots.

export const pad = (n: number) => String(n).padStart(2, '0')

/** "9", "09", "9:30", "930", "1730", "17.30" → "HH:mm"; null when not a time. */
export function parseTime(text: string): string | null {
  const t = text.trim().replace('.', ':')
  const m = t.match(/^(\d{1,2}):(\d{2})$/) ?? t.match(/^(\d{1,2})(\d{2})$/) ?? t.match(/^(\d{1,2})$/)
  if (!m) return null
  const h = Number(m[1])
  const min = m[2] === undefined ? 0 : Number(m[2])
  return h < 24 && min < 60 ? `${pad(h)}:${pad(min)}` : null
}

/** Whether a slot matches what the user typed: "9" and "09" both find 09:xx, "173" finds 17:30. */
export function matchesTime(slot: string, query: string): boolean {
  const q = query.trim().replace('.', ':')
  if (!q) return true
  const forms = [q]
  if (/^\d:/.test(q) || /^\d$/.test(q)) forms.push(`0${q}`)
  if (/^\d{3,4}$/.test(q)) forms.push(`${q.slice(0, 2)}:${q.slice(2)}`, `0${q.slice(0, 1)}:${q.slice(1)}`)
  return forms.some(f => slot.startsWith(f))
}

/** Every `step` minutes of the day as "HH:mm". */
export function timeSlots(step: number): string[] {
  return Array.from({ length: Math.ceil(1440 / step) }, (_, i) => `${pad(Math.floor((i * step) / 60))}:${pad((i * step) % 60)}`)
}
