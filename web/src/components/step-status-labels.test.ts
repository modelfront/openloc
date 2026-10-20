import { describe, it, expect } from 'vitest'
import type { StepStatusEntry } from '../api'
import { autoApprovedBeforeStep, stepEndStatusColor } from './step-status-labels'

const entry = (overrides: Partial<StepStatusEntry>): StepStatusEntry => ({
  step_name: 'Translation', step_order: 0, step_type: 'human', round: 1,
  end_reason: 'completed', status: 'approved', ai_outcome: null, edited: false,
  ended_at: '2026-10-14T10:00:00Z', ...overrides,
})

describe('autoApprovedBeforeStep', () => {
  it('keeps only the translations the previous step left auto-approved', () => {
    const previous = [
      { ...entry({ status: 'auto_approved' }), translation_id: 1 },
      { ...entry({ status: 'approved' }), translation_id: 2 },
      { ...entry({ status: 'needs_review' }), translation_id: 3 },
      { ...entry({ status: 'auto_approved', end_reason: 'rejected' }), translation_id: 4 },
    ]
    expect([...autoApprovedBeforeStep(previous)]).toEqual([1, 4])
  })
})

describe('stepEndStatusColor', () => {
  it('uses the Status column colours for confirmed results and grey otherwise', () => {
    expect(stepEndStatusColor('needs_review')).toBe('grey')
    expect(stepEndStatusColor('approved')).not.toBe('grey')
  })
})
