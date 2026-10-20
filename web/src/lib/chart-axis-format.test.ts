// Pinned to a NEGATIVE UTC offset, before the module under test is imported.
//
// This is the whole point of the file: `new Date('2026-09-14')` parses as UTC
// midnight, so a naive implementation is correct in Europe and off by one day
// in the Americas. Run these tests in a positive offset and every one of them
// passes against the bug — which is how the off-by-one would have shipped.
process.env.TZ = 'America/Los_Angeles'

import { describe, it, expect } from 'vitest'
import { grainOf, formatTick, formatTooltipLabel, tickGapFor, formatAxisNumber } from './chart-axis-format'

describe('grainOf', () => {
  it('recognises the three grains and nothing else', () => {
    expect(grainOf('day')).toBe('day')
    expect(grainOf('week')).toBe('week')
    expect(grainOf('month')).toBe('month')
    // A categorical axis must not be date-formatted.
    expect(grainOf('customer')).toBeNull()
    expect(grainOf('target_language')).toBeNull()
    expect(grainOf(undefined)).toBeNull()
  })
})

describe('formatTick', () => {
  it('shows day and month for a daily axis', () => {
    expect(formatTick('day', '2026-09-18')).toBe('18 Sep')
    // No zero padding: "3 Oct", not "03 Oct".
    expect(formatTick('day', '2026-10-03')).toBe('3 Oct')
  })

  it('shows the whole span for a weekly axis', () => {
    expect(formatTick('week', '2026-09-14')).toBe('14 Sep - 20 Sep')
  })

  it('carries the span across a month boundary', () => {
    expect(formatTick('week', '2026-09-28')).toBe('28 Sep - 4 Oct')
  })

  it('carries the span across a year boundary', () => {
    expect(formatTick('week', '2025-12-29')).toBe('29 Dec - 4 Jan')
  })

  it('shows the month alone for a monthly axis', () => {
    expect(formatTick('month', '2026-09-01')).toBe('Sep')
  })
})

describe('formatTooltipLabel', () => {
  it('adds the year on every grain', () => {
    expect(formatTooltipLabel('day', '2026-09-18')).toBe('18 Sep 2026')
    expect(formatTooltipLabel('week', '2026-09-14')).toBe('14 Sep - 20 Sep 2026')
    expect(formatTooltipLabel('month', '2026-09-01')).toBe('Sep 2026')
  })

  // The one case where one year is not enough to say which week this is.
  it('names both years for a week spanning New Year', () => {
    expect(formatTooltipLabel('week', '2025-12-29')).toBe('29 Dec 2025 - 4 Jan 2026')
  })
})

describe('bad input', () => {
  // A label is cosmetic; a chart must never blank because a value surprised it.
  it('passes through anything that is not a plain date', () => {
    expect(formatTick('day', 'Acme Ltd')).toBe('Acme Ltd')
    expect(formatTick('day', '')).toBe('')
    expect(formatTick('day', null)).toBe('')
    expect(formatTick('day', 42)).toBe('42')
    expect(formatTooltipLabel('month', '2026-09')).toBe('2026-09')
  })

  // The Date constructor rolls 30 February over to March; a label for a day
  // that does not exist is worse than showing the raw value.
  it('rejects a well-shaped but impossible date', () => {
    expect(formatTick('day', '2026-02-30')).toBe('2026-02-30')
    expect(formatTick('day', '2026-13-01')).toBe('2026-13-01')
  })

  it('accepts a real leap day', () => {
    expect(formatTick('day', '2028-02-29')).toBe('29 Feb')
  })
})

// The bug this module exists to avoid: `new Date('2026-09-14')` is UTC
// midnight, which is the 13th in any negative-offset zone.
describe('timezone safety', () => {
  it('keeps the calendar date the server sent', () => {
    expect(formatTick('day', '2026-09-14')).toBe('14 Sep')
    expect(formatTick('day', '2026-01-01')).toBe('1 Jan')
    expect(formatTooltipLabel('day', '2026-01-01')).toBe('1 Jan 2026')
  })
})

// A weekly label is a span, so it needs roughly twice the room of a single
// date and far more than a month name. One gap for all three reads as cramped
// on weeks or needlessly sparse on months.
describe('tickGapFor', () => {
  it('reserves the most room for the widest label', () => {
    expect(tickGapFor('week')).toBeGreaterThan(tickGapFor('day'))
    expect(tickGapFor('day')).toBeGreaterThan(tickGapFor('month'))
  })

  it('leaves room for a full weekly span at 11px', () => {
    // "31 Aug - 6 Sep" is ~75px; the gap has to be a real fraction of that or
    // recharts will happily overlap them.
    expect(tickGapFor('week')).toBeGreaterThanOrEqual(48)
  })
})

describe('formatAxisNumber', () => {
  it('keeps numbers below 10,000 in full, with separators', () => {
    expect(formatAxisNumber(0, 'en-US')).toBe('0')
    expect(formatAxisNumber(250, 'en-US')).toBe('250')
    expect(formatAxisNumber(2500, 'en-US')).toBe('2,500')
    expect(formatAxisNumber(9999, 'en-US')).toBe('9,999')
  })

  it('shortens 10,000 and up, so a six-digit tick is three or four characters', () => {
    expect(formatAxisNumber(10000, 'en-US')).toBe('10K')
    expect(formatAxisNumber(55000, 'en-US')).toBe('55K')
    expect(formatAxisNumber(110000, 'en-US')).toBe('110K')
    expect(formatAxisNumber(1250000, 'en-US')).toBe('1.3M')
    expect(formatAxisNumber(-55000, 'en-US')).toBe('-55K')
  })

  it('passes a non-number through untouched', () => {
    expect(formatAxisNumber('n/a', 'en-US')).toBe('n/a')
  })
})
