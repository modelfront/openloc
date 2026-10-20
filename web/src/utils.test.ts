import { describe, it, expect } from 'vitest'
import { formatDateTime } from './utils'

describe('formatDateTime', () => {
  it('returns "" for null, undefined or an unreadable date', () => {
    expect(formatDateTime(null)).toBe('')
    expect(formatDateTime(undefined)).toBe('')
    expect(formatDateTime('not a date')).toBe('')
  })

  it('returns a non-empty locale string for a valid ISO', () => {
    const out = formatDateTime('2026-09-15T17:00:00Z')
    // Locale-dependent content, but the year and an hour:minute time are always there.
    expect(out).toMatch(/2026/)
    expect(out).toMatch(/\d{1,2}:\d{2}/)
  })
})
