import { describe, it, expect } from 'vitest'
import { canRollback, defaultRollbackTarget, rerunChoice, rollbackBlockReason } from './rollback'
import type { StepType } from '../api'

const steps = (...types: StepType[]) => types.map((step_type, id) => ({ id, step_type }))

describe('rollbackBlockReason', () => {
  const wf = steps('pretranslate', 'human', 'ai', 'hybrid', 'human')

  it('allows earlier human and hybrid steps to everyone, and AI steps to managers', () => {
    expect(rollbackBlockReason(wf, 1, 4, false)).toBeUndefined()
    expect(rollbackBlockReason(wf, 3, 4, false)).toBeUndefined()
    expect(rollbackBlockReason(wf, 2, 4, false)).toBe('Only a manager can run the AI step again')
    expect(rollbackBlockReason(wf, 2, 4, true)).toBeUndefined()
  })

  it('never allows pre-translation, the current step or a later one', () => {
    expect(rollbackBlockReason(wf, 0, 4, true)).toBe('Pre-translation cannot be run again from here')
    expect(rollbackBlockReason(wf, 3, 3, true)).toBe('The current step')
    expect(rollbackBlockReason(wf, 4, 3, true)).toBe('Comes after the current step')
  })
})

describe('defaultRollbackTarget', () => {
  it('picks the nearest earlier human or hybrid step, skipping AI', () => {
    expect(defaultRollbackTarget(steps('human', 'ai', 'human'), 2)).toBe(0)
    expect(defaultRollbackTarget(steps('human', 'hybrid', 'human'), 2)).toBe(1)
    expect(defaultRollbackTarget(steps('pretranslate', 'ai', 'human'), 2)).toBeNull()
  })
})

describe('canRollback', () => {
  it('is false when nothing earlier can be picked', () => {
    expect(canRollback(steps('pretranslate', 'human'), 1, true)).toBe(false)
    expect(canRollback(steps('ai', 'human'), 1, false)).toBe(false)
    expect(canRollback(steps('ai', 'human'), 1, true)).toBe(true)
    expect(canRollback(steps('human'), 0, true)).toBe(false)
  })
})

describe('rerunChoice', () => {
  it('always runs an AI step, lets a manager choose on a hybrid one, never on a human one', () => {
    expect(rerunChoice({ id: 1, step_type: 'ai' }, false)).toBe('always')
    expect(rerunChoice({ id: 1, step_type: 'hybrid' }, true)).toBe('optional')
    expect(rerunChoice({ id: 1, step_type: 'hybrid' }, false)).toBe('never')
    expect(rerunChoice({ id: 1, step_type: 'human' }, true)).toBe('never')
    expect(rerunChoice(undefined, true)).toBe('never')
  })
})
