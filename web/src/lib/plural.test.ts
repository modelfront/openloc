import { describe, it, expect } from 'vitest'
import { count, matchCount } from './plural'

describe('count', () => {
  it('puts the noun in the plural unless the count is 1', () => {
    expect(count(0, 'member')).toBe('0 members')
    expect(count(1, 'member')).toBe('1 member')
    expect(count(59, 'member')).toBe('59 members')
    expect(count(null, 'member')).toBe('')
  })
})

describe('matchCount', () => {
  it('agrees the verb with the count', () => {
    expect(matchCount(1)).toBe('1 matches')
    expect(matchCount(0)).toBe('0 match')
    expect(matchCount(4)).toBe('4 match')
  })
})
