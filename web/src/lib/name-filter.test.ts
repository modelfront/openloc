import { describe, it, expect, vi, afterEach } from 'vitest'
import { filterByNameAndCreator, matchesName } from './name-filter'

describe('matchesName', () => {
  it('finds the text anywhere in the name, in any case', () => {
    expect(matchesName('Legal EN-DE 2024', 'en-de')).toBe(true)
    expect(matchesName('Legal EN-DE 2024', 'LEGAL')).toBe(true)
    expect(matchesName('Legal EN-DE 2024', 'medical')).toBe(false)
  })

  it('ignores accents on either side', () => {
    expect(matchesName('Médical FR', 'medical')).toBe(true)
    expect(matchesName('Medical FR', 'médical')).toBe(true)
    expect(matchesName('Straße', 'STRASSE')).toBe(false)
  })

  it('ignores spaces around the text, but not inside it', () => {
    expect(matchesName('Legal 2024', '  legal ')).toBe(true)
    expect(matchesName('Legal 2024', 'legal  2024')).toBe(false)
  })

  it('an empty or blank text matches every name', () => {
    expect(matchesName('Anything', '')).toBe(true)
    expect(matchesName('Anything', '   ')).toBe(true)
  })

  describe('in a browser set to Turkish', () => {
    // Turkish lower-cases "I" to a dotless "ı", so a locale-aware fold would
    // stop "medical" from finding "MEDICAL".
    afterEach(() => { vi.restoreAllMocks() })

    it('still ignores case', () => {
      const lower = String.prototype.toLocaleLowerCase
      vi.spyOn(String.prototype, 'toLocaleLowerCase').mockImplementation(function (this: string, locales?: Intl.LocalesArgument) {
        return lower.call(this, locales ?? 'tr')
      })
      expect(matchesName('MEDICAL FR', 'medical')).toBe(true)
      expect(matchesName('Legal EN-IT', 'it')).toBe(true)
    })
  })
})

describe('filterByNameAndCreator', () => {
  const rows = [
    { id: 1, name: 'Legal EN-DE', created_by: 7 },
    { id: 2, name: 'Legal EN-FR', created_by: 8 },
    { id: 3, name: 'Marketing', created_by: 7 },
    { id: 4, name: 'Legal archive', created_by: null },
  ]

  it('keeps only the rows that match both the name and the creator', () => {
    expect(filterByNameAndCreator(rows, 'legal', 7)?.map(r => r.id)).toEqual([1])
  })

  it('without a creator, filters on the name only', () => {
    expect(filterByNameAndCreator(rows, 'legal', null)?.map(r => r.id)).toEqual([1, 2, 4])
  })

  it('without a name, filters on the creator only', () => {
    expect(filterByNameAndCreator(rows, '', 7)?.map(r => r.id)).toEqual([1, 3])
  })

  it('passes a list that has not loaded yet through as undefined', () => {
    expect(filterByNameAndCreator(undefined, 'legal', null)).toBeUndefined()
  })
})
