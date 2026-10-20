import { describe, it, expect, vi, afterEach } from 'vitest'
import { formatDate } from '../utils'
import { cn } from './utils'

function isoAgo(ms: number): string {
  return new Date(Date.now() - ms).toISOString()
}

const SECOND = 1000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

describe('formatDate', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns "just now" for < 1 minute ago', () => {
    expect(formatDate(isoAgo(0))).toBe('just now')
    expect(formatDate(isoAgo(30 * SECOND))).toBe('just now')
    expect(formatDate(isoAgo(59 * SECOND))).toBe('just now')
  })

  it('returns minutes for 1-59 minutes ago', () => {
    expect(formatDate(isoAgo(1 * MINUTE))).toBe('1m ago')
    expect(formatDate(isoAgo(5 * MINUTE))).toBe('5m ago')
    expect(formatDate(isoAgo(59 * MINUTE))).toBe('59m ago')
  })

  it('returns hours for 1-23 hours ago', () => {
    expect(formatDate(isoAgo(1 * HOUR))).toBe('1h ago')
    expect(formatDate(isoAgo(12 * HOUR))).toBe('12h ago')
    expect(formatDate(isoAgo(23 * HOUR))).toBe('23h ago')
  })

  it('returns days for 1-6 days ago', () => {
    expect(formatDate(isoAgo(1 * DAY))).toBe('1d ago')
    expect(formatDate(isoAgo(3 * DAY))).toBe('3d ago')
    expect(formatDate(isoAgo(6 * DAY))).toBe('6d ago')
  })

  it('returns absolute date for 7+ days ago (same year)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-15T12:00:00Z'))
    const result = formatDate('2026-01-10T12:00:00Z')
    // Should show month + day, no year
    expect(result).toMatch(/Jan\s+10/)
    expect(result).not.toMatch(/2026/)
  })

  it('includes year for dates in a different year', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-15T12:00:00Z'))
    const result = formatDate('2025-03-20T12:00:00Z')
    expect(result).toMatch(/2025/)
  })

  it('handles boundary: exactly 60 minutes = 1h ago', () => {
    expect(formatDate(isoAgo(60 * MINUTE))).toBe('1h ago')
  })

  it('handles boundary: exactly 24 hours = 1d ago', () => {
    expect(formatDate(isoAgo(24 * HOUR))).toBe('1d ago')
  })

  it('handles boundary: exactly 7 days = absolute date', () => {
    const result = formatDate(isoAgo(7 * DAY))
    // Should NOT be "7d ago" — should be absolute
    expect(result).not.toContain('d ago')
  })

  it('does not crash on invalid ISO string', () => {
    const result = formatDate('not-a-date')
    // Invalid Date produces NaN diffs — should not throw
    expect(typeof result).toBe('string')
  })

  it('handles future dates gracefully', () => {
    const future = new Date(Date.now() + 5 * MINUTE).toISOString()
    const result = formatDate(future)
    expect(result).toBe('just now')
  })
})

describe('cn', () => {
  it('lets a caller override the custom h-input height', () => {
    // Control components put `h-input` on their box and merge the caller's
    // className after it. Unless tailwind-merge knows `input` is a spacing
    // value, both classes survive and the override loses to the cascade.
    expect(cn('h-input', 'h-7')).toBe('h-7')
    expect(cn('h-7', 'h-input')).toBe('h-input')
  })

  it('still resolves the standard groups', () => {
    expect(cn('w-full', 'w-40')).toBe('w-40')
    expect(cn('text-base md:text-sm', 'text-xs')).toBe('md:text-sm text-xs')
  })

  it('keeps classes from different groups', () => {
    expect(cn('h-input', 'w-40')).toBe('h-input w-40')
  })
})
