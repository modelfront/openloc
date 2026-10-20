import { describe, expect, it } from 'vitest'
import { TZDate } from '@date-fns/tz'
import { businessDaysFromNow, daysFromNow, hoursFromNow } from './date-presets'

// Friday 2026-09-25 14:37 in Tokyo (UTC+9, no DST) = 05:37Z.
const NOW = new TZDate(2026, 8, 25, 14, 37, 'Asia/Tokyo')
const utc = (d: Date) => new Date(d.getTime()).toISOString()

describe('date presets', () => {
  it('labels "N days" with singular/plural, and takes a custom label', () => {
    expect(daysFromNow(1).label).toBe('1 day')
    expect(daysFromNow(2).label).toBe('2 days')
    expect(businessDaysFromNow(3).label).toBe('3 business days')
    expect(hoursFromNow(1).label).toBe('1 hour')
    expect(daysFromNow(7, { label: '1 week' }).label).toBe('1 week')
  })

  it('daysFromNow adds days and rounds up to the next full hour', () => {
    expect(utc(daysFromNow(2).resolve(NOW))).toBe('2026-09-27T06:00:00.000Z') // 15:00 Tokyo
  })

  it('daysFromNow keeps an exact hour as is', () => {
    const onTheHour = new TZDate(2026, 8, 25, 14, 0, 'Asia/Tokyo')
    expect(utc(daysFromNow(1).resolve(onTheHour))).toBe('2026-09-26T05:00:00.000Z')
  })

  it('`at` sets the wall-clock time in the picker zone, not the browser zone', () => {
    // 17:00 Tokyo = 08:00Z
    expect(utc(daysFromNow(1, { at: '17:00' }).resolve(NOW))).toBe('2026-09-26T08:00:00.000Z')
    const berlin = new TZDate(2026, 8, 25, 14, 37, 'Europe/Berlin')
    // 17:00 Berlin (CEST, UTC+2) = 15:00Z
    expect(utc(daysFromNow(1, { at: '17:00' }).resolve(berlin))).toBe('2026-09-26T15:00:00.000Z')
  })

  it('daysFromNow keeps the wall-clock time across a DST change', () => {
    // Berlin leaves summer time on 2026-10-25; 17:00 CET is 16:00Z.
    const before = new TZDate(2026, 9, 24, 17, 0, 'Europe/Berlin')
    expect(utc(daysFromNow(1).resolve(before))).toBe('2026-10-25T16:00:00.000Z')
  })

  it('businessDaysFromNow skips the weekend', () => {
    // Friday + 1 business day = Monday 2026-09-28, 17:00 Tokyo.
    expect(utc(businessDaysFromNow(1, { at: '17:00' }).resolve(NOW))).toBe('2026-09-28T08:00:00.000Z')
  })

  it('hoursFromNow adds exact hours without rounding', () => {
    expect(utc(hoursFromNow(4).resolve(NOW))).toBe('2026-09-25T09:37:00.000Z')
  })
})
