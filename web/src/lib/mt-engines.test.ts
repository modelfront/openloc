import { describe, it, expect } from 'vitest'
import { combineMTEngines } from './mt-engines'

// Engines that cannot translate are REPORTED here, not filtered out,
// so a router rule whose engine is switched off still has
// an option to show.
describe('combineMTEngines', () => {
  it('keeps unusable engines and carries their reason', () => {
    const got = combineMTEngines(
      [
        { id: 1, name: 'DeepL', unusable: '' },
        { id: 2, name: 'Switched off', unusable: 'disabled' },
        { id: 3, name: 'Lost its key', unusable: 'no_key' },
      ],
      [{ id: 7, name: 'GPT', purpose: 'mt', unusable: 'disabled' }],
    )
    expect(got.map(e => [e.kind, e.id, e.unusable])).toEqual([
      ['mt', 1, ''],
      ['mt', 2, 'disabled'],
      ['mt', 3, 'no_key'],
      ['ai', 7, 'disabled'],
    ])
  })

  // QE+APE engines belong on workflow steps, never on an MT route —
  // that filter is a different rule and stays.
  it('still drops AI engines that are not mt-purpose', () => {
    const got = combineMTEngines([], [
      { id: 7, name: 'Reviewer', purpose: 'qe_ape', unusable: '' },
      { id: 8, name: 'Translator', purpose: 'mt', unusable: '' },
    ])
    expect(got.map(e => e.id)).toEqual([8])
  })

  // An engine with no `unusable` field at all reads as usable rather than
  // crashing a picker — the field arrived after these callers did.
  it('treats a missing reason as usable', () => {
    expect(combineMTEngines([{ id: 1, name: 'Old', enabled: true } as never], [])[0].unusable).toBe('')
  })
})
