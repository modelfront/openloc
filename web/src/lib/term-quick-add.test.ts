import { describe, expect, it } from 'vitest'
import { quickAddBlockedReason } from './term-quick-add'

const base = { id: 1, name: 'Main' }
const linguist = { isManager: false }
const manager = { isManager: true }

describe('quickAddBlockedReason', () => {
  it('stays closed, silently, while the list loads', () => {
    expect(quickAddBlockedReason(undefined, linguist)).toBe('')
  })

  it('says so when the list failed to load', () => {
    expect(quickAddBlockedReason(undefined, { ...linguist, failed: true })).toBe('The term bases could not be loaded. Try again.')
  })

  it('is closed, with the way out, when the project does not let this user add', () => {
    expect(quickAddBlockedReason({ term_bases: [base], can_add: false, linguist_status: 'pending' }, linguist))
      .toBe('Adding terms is turned off for this project. Ask a project manager to turn on Linguists can add terms.')
  })

  it('is closed when no term base allows Write, with advice for the role', () => {
    const none = { term_bases: [], can_add: true, linguist_status: 'pending' as const }
    expect(quickAddBlockedReason(none, manager))
      .toBe('No term base on this project takes new terms for this language pair. Add one, or tick Write on one, in the project settings.')
    expect(quickAddBlockedReason(none, linguist))
      .toBe('No term base on this project takes new terms for this language pair. Ask a project manager.')
  })

  it('is open with a writable base and the right to add', () => {
    expect(quickAddBlockedReason({ term_bases: [base], can_add: true, linguist_status: 'approved' }, linguist)).toBeNull()
  })
})
