import { describe, it, expect } from 'vitest'
import { matchesTime, parseTime, timeSlots } from './time-of-day'

describe('parseTime', () => {
  it.each([
    ['9', '09:00'],
    ['09', '09:00'],
    ['9:30', '09:30'],
    ['930', '09:30'],
    ['1730', '17:30'],
    ['17.30', '17:30'],
    [' 17:40 ', '17:40'],
    ['0:00', '00:00'],
    ['23:59', '23:59'],
  ])('reads %j as %s', (text, want) => {
    expect(parseTime(text)).toBe(want)
  })

  it.each(['', '24:00', '17:60', '99', 'abc', '1:5', '12345'])('rejects %j', text => {
    expect(parseTime(text)).toBeNull()
  })
})

describe('matchesTime', () => {
  it('matches by the start of the time, padding a one-digit hour', () => {
    expect(matchesTime('09:15', '9')).toBe(true)
    expect(matchesTime('19:15', '9')).toBe(false)
    expect(matchesTime('17:30', '17:3')).toBe(true)
    expect(matchesTime('17:30', '173')).toBe(true)
    expect(matchesTime('09:30', '930')).toBe(true)
    expect(matchesTime('13:00', '3')).toBe(false)
  })
})

describe('timeSlots', () => {
  it('covers the day in the given step', () => {
    const slots = timeSlots(15)
    expect(slots).toHaveLength(96)
    expect(slots.slice(0, 2)).toEqual(['00:00', '00:15'])
    expect(slots.at(-1)).toBe('23:45')
  })
})
