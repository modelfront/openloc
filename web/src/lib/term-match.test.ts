import { describe, it, expect } from 'vitest'
import { countWordOccurrences, termMatchState, conceptMatchState } from './term-match'

// These cases mirror internal/qa/checker_test.go so the client-side live
// underline agrees with the server-side QA verdict.

describe('countWordOccurrences', () => {
  it('counts whole-word occurrences case-insensitively', () => {
    expect(countWordOccurrences('The table and the TABLE.', 'table')).toBe(2)
  })

  it('does not match a sub-word (word boundary)', () => {
    // "Tabelle" must NOT be found inside the compound "Tabellerkennung".
    expect(countWordOccurrences('Die Tabellerkennung.', 'Tabelle')).toBe(0)
  })

  it('does not match a source sub-word either', () => {
    // "cat" only appears inside "category".
    expect(countWordOccurrences('Choose a category.', 'cat')).toBe(0)
  })

  it('returns 0 for an empty needle', () => {
    expect(countWordOccurrences('anything', '')).toBe(0)
  })

  it('matches flush against string edges', () => {
    expect(countWordOccurrences('table', 'table')).toBe(1)
  })

  it('does not count an apostrophe-glued fragment as a whole word', () => {
    // "company's" tokenizes to a bare "s", but it is a possessive fragment.
    expect(countWordOccurrences("a company's quality", 's')).toBe(0)
    // Contraction fragment likewise.
    expect(countWordOccurrences("I don't know", 'don')).toBe(0)
    // A genuine standalone unit symbol still counts.
    expect(countWordOccurrences('wait 5 s now', 's')).toBe(1)
  })
})

describe('termMatchState', () => {
  it('satisfied when target count >= source count', () => {
    expect(termMatchState('The table and the table.', 'Die Tabelle und die Tabelle.', 'table', 'Tabelle')).toBe('satisfied')
  })

  it('missing when target has fewer occurrences than source (undercount)', () => {
    const src = 'The provided table identifier is invalid. Please provide a valid table WID.'
    const tgt = 'Die angegebene Tabelle ist ungültig. Bitte geben Sie eine gültige Tabell-WID an.'
    expect(termMatchState(src, tgt, 'table', 'Tabelle')).toBe('missing')
  })

  it('missing when the target term is only a sub-word', () => {
    expect(termMatchState('The table.', 'Die Tabellerkennung.', 'table', 'Tabelle')).toBe('missing')
  })

  it('absent when the source term is only a sub-word (loose DB pre-filter)', () => {
    expect(termMatchState('Choose a category.', 'Wählen Sie eine Kategorie.', 'cat', 'Katze')).toBe('absent')
  })
})

// Mirrors internal/qa/checker_test.go's concept cases — must stay in lockstep.
describe('conceptMatchState (OR-within-concept)', () => {
  it('satisfied when ANY approved synonym is used', () => {
    expect(conceptMatchState('Reset your password', 'Setzen Sie Ihr Passwort zurück', ['password'], ['Passwort', 'Kennwort'])).toBe('satisfied')
  })

  it('missing when no approved synonym is present', () => {
    expect(conceptMatchState('Reset your password', 'Setzen Sie Ihr Schlüssel zurück', ['password'], ['Passwort', 'Kennwort'])).toBe('missing')
  })

  it('sums approved-synonym occurrences (2 source covered by one of each)', () => {
    const src = 'Enter your password, then confirm the password.'
    const tgt = 'Geben Sie Ihr Passwort ein, bestätigen Sie dann das Kennwort.'
    expect(conceptMatchState(src, tgt, ['password'], ['Passwort', 'Kennwort'])).toBe('satisfied')
  })

  it('uses the MAX over source synonyms to set the bar', () => {
    expect(conceptMatchState('The grid layout.', 'Das Layout.', ['table', 'grid'], ['Tabelle'])).toBe('missing')
    expect(conceptMatchState('The grid layout.', 'Das Tabelle-Layout.', ['table', 'grid'], ['Tabelle'])).toBe('satisfied')
  })

  it('absent when no source synonym occurs as a whole word', () => {
    expect(conceptMatchState('Choose a category.', 'Wählen Sie eine Kategorie.', ['cat'], ['Katze'])).toBe('absent')
  })
})
