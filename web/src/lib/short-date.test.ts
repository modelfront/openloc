import { describe, it, expect } from 'vitest'
import { dayMonth } from './short-date'

describe('dayMonth', () => {
  it('shows the day and the month, without the year', () => {
    expect(dayMonth('2026-11-03T12:00:00Z')).toBe('Nov 3')
  })
})
