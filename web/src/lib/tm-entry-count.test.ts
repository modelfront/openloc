import { describe, it, expect } from 'vitest'
import { tmEntryCountLabel } from './tm-entry-count'

describe('tmEntryCountLabel', () => {
  it('shows loaded of total when the total is known', () => {
    expect(tmEntryCountLabel(200, 5000, false, true)).toBe('200 of 5,000 entries')
    expect(tmEntryCountLabel(3, 3, true, false)).toBe('3 of 3 matching entries')
  })

  it('shows what is loaded, with + while more pages wait, when the total is unknown', () => {
    expect(tmEntryCountLabel(200, undefined, true, true)).toBe('200+ matching entries')
    expect(tmEntryCountLabel(42, undefined, true, false)).toBe('42 matching entries')
  })

  it('says 0 when nothing matches', () => {
    expect(tmEntryCountLabel(0, 0, true, false)).toBe('0 matching entries')
    expect(tmEntryCountLabel(0, 0, false, false)).toBe('0 entries')
  })
})
