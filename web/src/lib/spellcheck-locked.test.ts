// @vitest-environment node
import { describe, it, expect } from 'vitest'
import {
  selectScopeTargets, countScope, resolveRunScope, scopeIncludesLocked, allowedRunScope, DEFAULT_RUN_SCOPE,
  type ScopeCandidate, type SpellcheckScope,
} from './spellcheck-scope'

// Spell check skips locked segments unless the user picks "All segments
// (includes locked)". "Non-locked segments" is the default, and the all scope
// is for owners and admins only.
const rows: ScopeCandidate[] = [
  { id: 1, active_text: 'Bitte die adresse prüfen.' },
  { id: 2, active_text: 'Die adresse ist gesperrt.', locked: true },
  { id: 3, active_text: 'Das Paket geht an die adresse.' },
]
// segment id = translation id × 10
const bySegment = new Map(rows.map(r => [r.id * 10, r]))
const run = (scope: Parameters<typeof resolveRunScope>[0], selected = new Set<number>()) =>
  resolveRunScope(scope, [0, 1, 2], [10, 20, 30], bySegment, selected)

describe('spell check and locked segments', () => {
  it('the review dialog opens on "Non-locked segments"', () => {
    expect(DEFAULT_RUN_SCOPE).toBe('nonlocked')
  })

  it('only "all" includes locked segments', () => {
    const scopes: SpellcheckScope[] = ['segment', 'all', 'nonlocked', 'filtered', 'selected']
    expect(scopes.filter(scopeIncludesLocked)).toEqual(['all'])
  })

  it('"In non-locked segments" in the fix panel writes no locked row', () => {
    expect(selectScopeTargets(rows, 'adresse', 'nonlocked').map(r => r.id)).toEqual([1, 3])
  })

  it('"In all segments" in the fix panel writes the locked row too', () => {
    expect(selectScopeTargets(rows, 'adresse', 'all').map(r => r.id)).toEqual([1, 2, 3])
  })

  it('counts each scope the way it writes', () => {
    expect(countScope(rows, 'adresse')).toEqual({ all: 3, nonlocked: 2, locked: 1 })
  })

  it('"Non-locked segments" in the review dialog checks no locked row', () => {
    expect(run('nonlocked')).toEqual([1, 3])
  })

  it('"All segments" in the review dialog checks the locked row too', () => {
    expect(run('all')).toEqual([1, 2, 3])
  })

  it('the filtered and selected scopes skip locked rows', () => {
    expect(run('filtered')).toEqual([1, 3])
    expect(run('selected', new Set([1, 2]))).toEqual([1])
  })

  // The server writes through a lock for owners and admins only, so the
  // dialog never opens on "All segments" for anyone else.
  it('"All segments" is for owners and admins; anyone else opens on the default', () => {
    expect(allowedRunScope('all', true)).toBe('all')
    expect(allowedRunScope('all', false)).toBe(DEFAULT_RUN_SCOPE)
    expect(allowedRunScope('selected', false)).toBe('selected')
  })
})
