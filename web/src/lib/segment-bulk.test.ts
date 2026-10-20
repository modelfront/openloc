import { describe, it, expect } from 'vitest'
import { heldSkipNote } from './segment-bulk'

describe('heldSkipNote', () => {
  it('says nothing when no segment was held', () => {
    expect(heldSkipNote(0)).toBeNull()
    expect(heldSkipNote(undefined)).toBeNull()
  })

  it('counts the segments someone else has open', () => {
    expect(heldSkipNote(1)).toBe('1 segment was skipped because someone else is editing it.')
    expect(heldSkipNote(3)).toBe('3 segments were skipped because someone else is editing them.')
  })
})
