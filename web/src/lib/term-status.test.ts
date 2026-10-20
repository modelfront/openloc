import { describe, it, expect } from 'vitest'
import { termStatusLabel } from './term-status'

describe('term-status', () => {
  it('maps each status to its sentence-case label', () => {
    expect(termStatusLabel('approved')).toBe('Approved')
    expect(termStatusLabel('forbidden')).toBe('Forbidden')
    expect(termStatusLabel('pending')).toBe('Pending')
  })

  it('falls back to the raw value for unknown/empty statuses', () => {
    expect(termStatusLabel('archived')).toBe('archived')
    expect(termStatusLabel('')).toBe('')
    expect(termStatusLabel(null)).toBe('')
    expect(termStatusLabel(undefined)).toBe('')
  })
})
