import { describe, it, expect } from 'vitest'
import { modelsEmptyText } from './model-selection'
import type { AIVendorStatus } from '../../api'

// An empty model dropdown has two very different causes, and the user can only
// act on one of them. Each gets its own sentence, so a rejected key never looks
// identical to a key with nothing to offer.
describe('modelsEmptyText', () => {
  const vendor = (over: Partial<AIVendorStatus> = {}) =>
    ({ provider: 'claude', connected: true, tokens: [], connections: [], models: [], ...over }) as AIVendorStatus

  it('explains a failed vendor call so the user knows the key is the problem', () => {
    expect(modelsEmptyText(vendor({ models_error: 'The API key was rejected.' })))
      .toBe('The API key was rejected.')
  })

  it('falls back to the generic line when nothing failed', () => {
    expect(modelsEmptyText(vendor())).toBe('No models available for this key')
  })

  it('handles a vendor that has not loaded yet', () => {
    expect(modelsEmptyText(undefined)).toBe('No models available for this key')
  })
})
