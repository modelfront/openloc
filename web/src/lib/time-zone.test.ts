import { describe, expect, it } from 'vitest'
import { timeZoneOffsetLabel, timeZoneOptions } from './time-zone'

describe('time zones', () => {
  it('labels the offset for the given instant, following DST', () => {
    expect(timeZoneOffsetLabel('Europe/Berlin', new Date('2026-07-01T12:00:00Z'))).toBe('GMT+2')
    expect(timeZoneOffsetLabel('Europe/Berlin', new Date('2026-12-01T12:00:00Z'))).toBe('GMT+1')
    expect(timeZoneOffsetLabel('Asia/Kolkata', new Date('2026-07-01T12:00:00Z'))).toBe('GMT+5:30')
    expect(timeZoneOffsetLabel('UTC')).toBe('GMT')
  })

  it('lists zones sorted, with a readable name, the offset chip and searchable keywords', () => {
    const opts = timeZoneOptions(new Date('2026-07-01T12:00:00Z'))
    const ny = opts.find(o => o.value === 'America/New_York')
    expect(ny).toEqual({
      value: 'America/New_York',
      label: 'America / New York',
      chip: 'GMT-4',
      keywords: 'America/New_York GMT-4',
    })
    expect(opts.some(o => o.value === 'UTC')).toBe(true)
    const values = opts.map(o => o.value)
    expect(values).toEqual([...values].sort())
    expect(new Set(values).size).toBe(values.length)
  })

  it('shares one list between callers, but never across an offset change', () => {
    const summer = new Date('2026-07-01T12:00:00Z')
    const first = timeZoneOptions(summer)
    expect(timeZoneOptions(new Date(summer.getTime() + 30_000)), 'rebuilt within the same minute').toBe(first)

    const berlin = (opts: ReturnType<typeof timeZoneOptions>) => opts.find(o => o.value === 'Europe/Berlin')?.chip
    expect(berlin(first)).toBe('GMT+2')
    expect(berlin(timeZoneOptions(new Date('2026-12-01T12:00:00Z'))), 'stale summer offset served in winter').toBe('GMT+1')
  })
})
