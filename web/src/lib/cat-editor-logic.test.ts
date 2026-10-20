import { describe, it, expect } from 'vitest'
import {
  isTermApplied,
  computeRepetitionSegmentIds,
  computeRepetitionSourceIds,
  computeRepetitionStats,
} from './cat-editor-logic'

// isTermApplied is count-aware: applied ⟺ the target
// carries the term at least as many times as the source uses it, word-boundary
// matched — the SAME predicate as the green source-term underline. Signature is
// (sourceText, translationText, sourceTerm, targetTerm).
describe('isTermApplied (count-aware)', () => {
  it('true when the target carries the term as often as the source (1:1)', () => {
    expect(isTermApplied('Reset your password.', 'Setzen Sie Ihr Passwort zurück.', 'password', 'Passwort')).toBe(true)
  })

  it('case-insensitive', () => {
    expect(isTermApplied('Reset your PASSWORD.', 'ihr passwort zurück', 'password', 'Passwort')).toBe(true)
  })

  it('false when the target term is absent', () => {
    expect(isTermApplied('Reset your password.', 'Setzen Sie Ihr Kennwort zurück.', 'password', 'Passwort')).toBe(false)
  })

  it('false when nothing matches', () => {
    expect(isTermApplied('Enter your password', 'Hallo Welt', 'password', 'Kennwort')).toBe(false)
  })

  it('true when source term equals target (loanword) and both present', () => {
    expect(isTermApplied('Please login', 'Bitte Login eingeben', 'login', 'Login')).toBe(true)
  })

  it('false on empty translation', () => {
    expect(isTermApplied('Enter password', '', 'password', 'Passwort')).toBe(false)
  })

  it('multi-word target term', () => {
    expect(isTermApplied('Enter your email address', 'Geben Sie Ihre E-Mail-Adresse ein', 'email address', 'E-Mail-Adresse')).toBe(true)
  })

  it('false when the old (wrong) translation is still there', () => {
    expect(isTermApplied('Protect your data', 'Ihre Daten schützen', 'data', 'Datensatz')).toBe(false)
  })

  // 2 source occurrences, only 1 translated → NOT applied,
  // so the row stays clickable (no ✓) and the second occurrence shows red.
  it('false when the target has FEWER occurrences than the source', () => {
    const src = 'Enter your new password, repeat your password to verify.'
    const tgt = 'Geben Sie Ihr neues Passwort ein, wiederholen Sie Ihr Passwortt.'
    expect(isTermApplied(src, tgt, 'password', 'Passwort')).toBe(false)
  })

  it('true once every source occurrence is covered', () => {
    const src = 'Enter your password, repeat your password.'
    const tgt = 'Passwort eingeben, Passwort wiederholen.'
    expect(isTermApplied(src, tgt, 'password', 'Passwort')).toBe(true)
  })

  // Word boundary (item #2): a sub-word compound is not a real occurrence.
  it('false when only a sub-word compound matches (Tabellerkennung)', () => {
    expect(isTermApplied('The table.', 'Die Tabellerkennung.', 'table', 'Tabelle')).toBe(false)
  })
})

// editor's Match column and the repetition band totals both key off this
// predicate. Changing the rule here ripples to every per-segment row +
// every aggregate count in the analysis tab — keep the contract pinned.
describe('computeRepetitionSegmentIds', () => {
  const seg = (id: number, text: string) => ({ id, source_text: text })

  it('returns empty for empty / nullish input', () => {
    expect(computeRepetitionSegmentIds([])).toEqual(new Set())
    expect(computeRepetitionSegmentIds(undefined)).toEqual(new Set())
    expect(computeRepetitionSegmentIds(null)).toEqual(new Set())
  })

  it('marks NO segments when every source is unique', () => {
    const result = computeRepetitionSegmentIds([
      seg(1, 'Hello'),
      seg(2, 'World'),
      seg(3, 'Goodbye'),
    ])
    expect(result.size).toBe(0)
  })

  it('marks ONLY the subsequent occurrences — the first is the canonical', () => {
    const result = computeRepetitionSegmentIds([
      seg(1, 'Hello'),
      seg(2, 'World'),
      seg(3, 'Hello'),   // second occurrence → repetition
      seg(4, 'Hello'),   // third → repetition
      seg(5, 'World'),   // second occurrence of World → repetition
    ])
    expect(result.has(1)).toBe(false) // canonical
    expect(result.has(2)).toBe(false) // canonical
    expect(result.has(3)).toBe(true)
    expect(result.has(4)).toBe(true)
    expect(result.has(5)).toBe(true)
    expect(result.size).toBe(3)
  })

  it('preserves array order — earlier-id segments are NOT necessarily the canonical', () => {
    // If segment 5 appears in the array BEFORE segment 2 (e.g. due to file
    // re-ordering), the array-order one wins. The function shouldn't sort by
    // id — it walks in the order given, which is the file's position order.
    const result = computeRepetitionSegmentIds([
      seg(5, 'Hello'),
      seg(2, 'Hello'), // appears second in the array → repetition, despite lower id
    ])
    expect(result.has(5)).toBe(false)
    expect(result.has(2)).toBe(true)
  })

  it('treats case differences as distinct (matches PropagateTranslation SQL)', () => {
    const result = computeRepetitionSegmentIds([
      seg(1, 'Hello'),
      seg(2, 'hello'),   // different case → not a repetition
      seg(3, 'HELLO'),   // different case → not a repetition
    ])
    expect(result.size).toBe(0)
  })

  it('treats whitespace-different sources as distinct', () => {
    const result = computeRepetitionSegmentIds([
      seg(1, 'Hello'),
      seg(2, ' Hello'),  // leading space → not a repetition
      seg(3, 'Hello '),  // trailing space → not a repetition
    ])
    expect(result.size).toBe(0)
  })

  it('handles empty strings — empty matches empty', () => {
    const result = computeRepetitionSegmentIds([
      seg(1, ''),
      seg(2, ''),  // empty string repeats empty string
      seg(3, 'X'),
    ])
    expect(result.has(2)).toBe(true)
    expect(result.size).toBe(1)
  })
})

// computeRepetitionSourceIds is the mirror of computeRepetitionSegmentIds —
// it returns the FIRST occurrences that have duplicates downstream. The source
// segment ("→R" badge) is distinguished from
// the propagated targets ("⇢R" badge). The two sets are disjoint by
// construction; segments with unique sources are in neither.
describe('computeRepetitionSourceIds', () => {
  const seg = (id: number, text: string) => ({ id, source_text: text })

  it('returns empty for empty / nullish input', () => {
    expect(computeRepetitionSourceIds([])).toEqual(new Set())
    expect(computeRepetitionSourceIds(undefined)).toEqual(new Set())
    expect(computeRepetitionSourceIds(null)).toEqual(new Set())
  })

  it('returns empty when every source is unique', () => {
    const result = computeRepetitionSourceIds([
      seg(1, 'Hello'),
      seg(2, 'World'),
      seg(3, 'Goodbye'),
    ])
    expect(result.size).toBe(0)
  })

  it('marks ONLY the first occurrence — duplicates are not sources', () => {
    const result = computeRepetitionSourceIds([
      seg(1, 'Hello'),
      seg(2, 'World'),
      seg(3, 'Hello'),  // 2nd occurrence of Hello — NOT a source
      seg(4, 'Hello'),  // 3rd  — NOT a source
      seg(5, 'World'),  // 2nd occurrence of World — NOT a source
    ])
    expect(result.has(1)).toBe(true)  // first occurrence of Hello → source
    expect(result.has(2)).toBe(true)  // first occurrence of World → source
    expect(result.has(3)).toBe(false)
    expect(result.has(4)).toBe(false)
    expect(result.has(5)).toBe(false)
    expect(result.size).toBe(2)
  })

  it('disjoint from computeRepetitionSegmentIds (the two sets partition the repeated rows)', () => {
    const data = [
      seg(1, 'Hello'),
      seg(2, 'World'),
      seg(3, 'Hello'),
      seg(4, 'Hello'),
      seg(5, 'Goodbye'),  // unique — in neither set
      seg(6, 'World'),
    ]
    const sources = computeRepetitionSourceIds(data)
    const targets = computeRepetitionSegmentIds(data)
    // Disjoint
    for (const id of sources) {
      expect(targets.has(id), `source ${id} must not also be a repetition target`).toBe(false)
    }
    // Together cover all duplicate rows (everything except unique sources)
    expect([...sources, ...targets].sort()).toEqual([1, 2, 3, 4, 6])
    expect(sources.has(5)).toBe(false)
    expect(targets.has(5)).toBe(false)
  })

  it('a segment with only ITSELF (no duplicates) is NOT a source', () => {
    // A first occurrence is only "a source" if at least one duplicate exists
    // later in the file. Otherwise it's just a unique segment.
    const result = computeRepetitionSourceIds([
      seg(1, 'Hello'),  // unique — not a source
      seg(2, 'World'),  // unique — not a source
    ])
    expect(result.size).toBe(0)
  })

  it('case + whitespace differences keep sources unique', () => {
    const result = computeRepetitionSourceIds([
      seg(1, 'Hello'),
      seg(2, 'hello'),
      seg(3, ' Hello'),
    ])
    expect(result.size).toBe(0)
  })
})

// computeRepetitionStats — totals shown in the CAT editor's leverage status
// bar's Repetitions band. Counts only SUBSEQUENT occurrences (mirrors
// `computeRepetitionSegmentIds`); word count uses plain whitespace-split.
describe('computeRepetitionStats', () => {
  const seg = (id: number, text: string) => ({ id, source_text: text })

  it('zeros for empty / nullish input', () => {
    expect(computeRepetitionStats([])).toEqual({ segments: 0, words: 0, characters: 0 })
    expect(computeRepetitionStats(undefined)).toEqual({ segments: 0, words: 0, characters: 0 })
    expect(computeRepetitionStats(null)).toEqual({ segments: 0, words: 0, characters: 0 })
  })

  it('zeros when every source is unique', () => {
    expect(computeRepetitionStats([
      seg(1, 'Hello world'),
      seg(2, 'Different text'),
    ])).toEqual({ segments: 0, words: 0, characters: 0 })
  })

  it('counts subsequent occurrences only (first is canonical, not counted)', () => {
    // "Hello world" repeats twice: 2 subsequent occurrences × 2 words = 4 words.
    // Characters: "Hello world".length = 11 × 2 = 22.
    const result = computeRepetitionStats([
      seg(1, 'Hello world'),  // canonical — NOT counted
      seg(2, 'unique text'),
      seg(3, 'Hello world'),  // 1st repetition — counted
      seg(4, 'Hello world'),  // 2nd repetition — counted
    ])
    expect(result).toEqual({ segments: 2, words: 4, characters: 22 })
  })

  it('aggregates across multiple repeated sources', () => {
    // Two groups: "abc" repeats once (1 seg, 1 word, 3 chars), "def ghi"
    // repeats once (1 seg, 2 words, 7 chars). Totals: 2, 3, 10.
    const result = computeRepetitionStats([
      seg(1, 'abc'),
      seg(2, 'def ghi'),
      seg(3, 'abc'),
      seg(4, 'def ghi'),
    ])
    expect(result).toEqual({ segments: 2, words: 3, characters: 10 })
  })

  it('empty source counts as 0 words but 0 characters too', () => {
    const result = computeRepetitionStats([
      seg(1, ''),
      seg(2, ''),  // subsequent empty → counts segments but 0 words/chars
    ])
    expect(result).toEqual({ segments: 1, words: 0, characters: 0 })
  })

  it('whitespace-only source counts as 0 words', () => {
    const result = computeRepetitionStats([
      seg(1, '   '),
      seg(2, '   '),  // 3 chars but 0 words after trim
    ])
    expect(result).toEqual({ segments: 1, words: 0, characters: 3 })
  })

  it('handles multiple whitespace between words', () => {
    const result = computeRepetitionStats([
      seg(1, 'one  two   three'),  // 16 chars, 3 words
      seg(2, 'one  two   three'),
    ])
    expect(result.words).toBe(3)
    expect(result.characters).toBe(16)
  })
})

