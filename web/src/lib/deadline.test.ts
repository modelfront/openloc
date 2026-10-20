import { describe, it, expect } from 'vitest'
import { formatDeadline } from './deadline'

describe('formatDeadline', () => {
  it('returns "" for null/undefined', () => {
    expect(formatDeadline(null)).toBe('')
    expect(formatDeadline(undefined)).toBe('')
  })

  it('returns a non-empty locale string for a valid ISO', () => {
    const out = formatDeadline('2026-09-15T17:00:00Z')
    expect(out.length).toBeGreaterThan(0)
    // Locale-dependent content, but year + 2026 should always be present.
    expect(out).toMatch(/2026/)
  })

  it("ends with the browser zone's offset, so the reader knows which zone the time is in", () => {
    expect(formatDeadline('2026-09-15T17:00:00Z')).toMatch(/ (GMT|UTC)([+-]\d{1,2}(:\d{2})?)?$/)
  })
})

