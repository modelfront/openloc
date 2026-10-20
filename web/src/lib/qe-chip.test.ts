import { describe, it, expect } from 'vitest'
import { qeChipLabel } from './qe-chip'

describe('qeChipLabel', () => {
  it('shows a short score, else just "QE", and nothing without a note', () => {
    expect(qeChipLabel('0.92')).toBe('QE 0.92')
    expect(qeChipLabel('a long free-text note')).toBe('QE')
    expect(qeChipLabel('')).toBeNull()
    expect(qeChipLabel(null)).toBeNull()
  })
})
