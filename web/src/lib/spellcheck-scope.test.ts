// @vitest-environment node
import { describe, it, expect } from 'vitest'
import {
  wordOccurrences, wordOffsets, containsWord, selectScopeTargets, countScope, type ScopeCandidate,
} from './spellcheck-scope'

const rows: ScopeCandidate[] = [
  { id: 1, active_text: 'Bitte die adresse prüfen.' },
  { id: 2, active_text: 'Das Paket geht an die adresse.' },
  { id: 3, active_text: 'Die adresse bleibt gleich.' },
  { id: 4, active_text: 'Die adresse ist gesperrt.', locked: true },
  { id: 5, active_text: 'Keine Fehler hier.' },
  // Substring, NOT the word — must never be rewritten.
  { id: 6, active_text: 'Die Lieferadresse ist korrekt.' },
]

describe('wordOccurrences', () => {
  it('counts whole words only', () => {
    expect(wordOccurrences('die adresse hier', 'adresse')).toBe(1)
    expect(wordOccurrences('die adresse und die adresse', 'adresse')).toBe(2)
  })

  // The bug this guards: `Lieferadresse` contains `adresse`, but replacing it
  // would corrupt a correctly-spelled compound. German compounds make this
  // common rather than exotic.
  it('does not match inside a compound word', () => {
    expect(wordOccurrences('Die Lieferadresse ist korrekt.', 'adresse')).toBe(0)
    expect(wordOccurrences('Postadresse', 'adresse')).toBe(0)
  })

  it('treats punctuation as a boundary', () => {
    expect(wordOccurrences('an die adresse.', 'adresse')).toBe(1)
    expect(wordOccurrences('(adresse)', 'adresse')).toBe(1)
    expect(wordOccurrences('"adresse",', 'adresse')).toBe(1)
  })

  // `\b` would treat an umlaut as a boundary and match the compound's tail.
  it('treats accented letters as word characters, not boundaries', () => {
    expect(wordOccurrences('Prüfadresse', 'adresse')).toBe(0)
    expect(wordOccurrences('die Grüße adresse', 'adresse')).toBe(1)
  })

  it('is case-sensitive, matching the dictionary and the server', () => {
    expect(wordOccurrences('Die Adresse hier', 'adresse')).toBe(0)
  })

  it('handles empty inputs', () => {
    expect(wordOccurrences('', 'adresse')).toBe(0)
    expect(wordOccurrences('text', '')).toBe(0)
    expect(containsWord('die adresse', 'adresse')).toBe(true)
  })
})

describe('countScope', () => {
  // One number per fix-panel button: "In all segments" counts locked rows,
  // "In non-locked segments" does not.
  it('counts every occurrence, splitting locked from non-locked', () => {
    expect(countScope(rows, 'adresse')).toEqual({ all: 4, nonlocked: 3, locked: 1 })
  })

  it('all always equals nonlocked plus locked', () => {
    const c = countScope(rows, 'adresse')
    expect(c.all).toBe(c.nonlocked + c.locked)
  })

  it('ignores compounds and clean rows', () => {
    expect(countScope([rows[5], rows[4]], 'adresse')).toEqual({ all: 0, nonlocked: 0, locked: 0 })
  })
})

describe('selectScopeTargets', () => {
  it('returns nothing for segment scope — the editor handles that row', () => {
    expect(selectScopeTargets(rows, 'adresse', 'segment', 1)).toEqual([])
  })

  it('all scope covers every occurrence except the open segment, locked included', () => {
    const ids = selectScopeTargets(rows, 'adresse', 'all', 1).map(r => r.id)
    expect(ids).toEqual([2, 3, 4])
  })

  it('nonlocked scope drops locked rows', () => {
    const ids = selectScopeTargets(rows, 'adresse', 'nonlocked', 1).map(r => r.id)
    expect(ids).toEqual([2, 3])
  })

  it('nonlocked count matches what the nonlocked scope writes', () => {
    const counts = countScope(rows, 'adresse')
    const written = selectScopeTargets(rows, 'adresse', 'nonlocked')
    expect(written.length).toBe(counts.nonlocked)
  })

  it('never targets a compound match, in either scope', () => {
    for (const scope of ['all', 'nonlocked'] as const) {
      expect(selectScopeTargets(rows, 'adresse', scope).map(r => r.id)).not.toContain(6)
    }
  })

  it('includes the open segment when no exclusion is given', () => {
    const ids = selectScopeTargets(rows, 'adresse', 'all').map(r => r.id)
    expect(ids).toEqual([1, 2, 3, 4])
  })

  // The counts on the buttons must match what gets written, or the toast lies.
  it('button count minus the open segment equals the rows written', () => {
    const counts = countScope(rows, 'adresse')
    const written = selectScopeTargets(rows, 'adresse', 'all', 1)
    expect(written.length).toBe(counts.all - 1)
  })
})

describe('wordOffsets', () => {
  it('returns each whole-word offset', () => {
    const text = 'die adresse und die adresse'
    expect(wordOffsets(text, 'adresse')).toEqual([4, 20])
  })

  it('skips compound matches', () => {
    expect(wordOffsets('Die Lieferadresse und die adresse', 'adresse')).toEqual([26])
  })

  it('agrees with wordOccurrences', () => {
    const text = 'adresse, adresse; Postadresse (adresse)'
    expect(wordOffsets(text, 'adresse')).toHaveLength(wordOccurrences(text, 'adresse'))
  })
})
