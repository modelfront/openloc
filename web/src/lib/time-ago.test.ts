import { describe, it, expect, vi, afterEach } from 'vitest'
import { timeAgo } from './time-ago'

const NOW = new Date('2026-09-11T12:00:00Z')

function at(iso: string): string {
  vi.setSystemTime(NOW)
  return timeAgo(iso)
}

afterEach(() => vi.useRealTimers())

describe('timeAgo', () => {
  it('reads "just now" under a minute', () => {
    vi.useFakeTimers()
    expect(at('2026-09-11T11:59:30Z')).toBe('just now')
  })

  it('counts minutes, then hours, then days', () => {
    vi.useFakeTimers()
    expect(at('2026-09-11T11:45:00Z')).toBe('15m ago')
    expect(at('2026-09-11T09:00:00Z')).toBe('3h ago')
    expect(at('2026-09-09T12:00:00Z')).toBe('2d ago')
  })

  it('switches to months, then years', () => {
    vi.useFakeTimers()
    expect(at('2026-08-02T12:00:00Z')).toBe('1mo ago')   // 40 days
    expect(at('2026-03-11T12:00:00Z')).toBe('6mo ago')   // 184 days
    expect(at('2025-09-11T12:00:00Z')).toBe('1y ago')    // 365 days
    expect(at('2024-09-11T12:00:00Z')).toBe('2y ago')
  })

  // 360 days is 12 whole months but 0 whole years. Branching on the month
  // count instead of the day count prints "0y ago" here.
  it('does not print "0y ago" between 12 months and a year', () => {
    vi.useFakeTimers()
    expect(at('2025-09-16T12:00:00Z')).toBe('12mo ago')  // 360 days
  })

  it('holds the day unit right up to the month boundary', () => {
    vi.useFakeTimers()
    expect(at('2026-08-13T12:00:00Z')).toBe('29d ago')
    expect(at('2026-08-12T12:00:00Z')).toBe('1mo ago')   // 30 days
  })

  // Server and browser clocks drift by seconds. "-1m ago" in the sidebar would
  // look like a bug in the history, not in the clock.
  it('treats a future timestamp as "just now"', () => {
    vi.useFakeTimers()
    expect(at('2026-09-11T12:05:00Z')).toBe('just now')
  })

  it('does not render NaN for an unparseable date', () => {
    vi.useFakeTimers()
    expect(at('not a date')).toBe('just now')
  })
})
