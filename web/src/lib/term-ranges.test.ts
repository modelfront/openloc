import { describe, it, expect } from 'vitest'
import { termRanges, normalizeTerm, UNDERLINE_GREEN, UNDERLINE_RED, UNDERLINE_NEUTRAL } from './term-ranges'
import type { TermLookupResult } from '../api/terminology'

const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
  id: 1, concept_id: 1, source_term: 'password', target_term: 'Passwort', mt_translation: '',
  status: 'approved', notes: '', domain: '', term_base_id: 1,
  term_base_name: 'tb', priority: 0, ...over,
})

/** Convenience: the matched substrings, in document order. */
const matched = (text: string, ranges: { start: number; end: number }[]) =>
  ranges.map(r => text.slice(r.start, r.end))

describe('normalizeTerm', () => {
  it('lowercases and collapses separator runs to a single space', () => {
    expect(normalizeTerm('Terms  of\nService')).toBe('terms of service')
    expect(normalizeTerm('Terms of\u00A0Service')).toBe('terms of service')
    expect(normalizeTerm('Node.js')).toBe('node js')
  })

  it('preserves hyphens and underscores, which the server tokenizer keeps inside words', () => {
    expect(normalizeTerm('Two-Factor Authentication')).toBe('two-factor authentication')
    expect(normalizeTerm('api_key')).toBe('api_key')
  })
})

describe('termRanges', () => {
  it('returns nothing for empty input', () => {
    expect(termRanges('Enter your password', [])).toEqual([])
    expect(termRanges('', [term()])).toEqual([])
  })

  it('reports the offsets of a single-word match', () => {
    const ranges = termRanges('Enter your password now', [term()], 'Passwort eingeben')
    expect(ranges.length).toBe(1)
    expect(ranges[0].start).toBe(11)
    expect(ranges[0].end).toBe(19)
    expect(ranges[0].term.target_term).toBe('Passwort')
  })

  // The server FINDS terms by tokenizing the source text (tokenizeSourceText in
  // internal/terminology/handler.go): punctuation/symbols become spaces, any
  // whitespace run splits words, n-grams are joined with ONE space. So a term
  // the sidebar lists can sit in the raw text with a newline / NBSP / comma
  // between its words — the highlighter has to match those too.
  describe('separator tolerance (matches what the server tokenizer found)', () => {
    const tos = term({ source_term: 'terms of service', target_term: 'Nutzungsbedingungen' })
    const cases: [string, string][] = [
      ['single space', 'Read the Terms of Service carefully.'],
      ['newline', 'Read the Terms of\nService carefully.'],
      ['double space', 'Read the Terms of  Service carefully.'],
      ['non-breaking space', 'Read the Terms of\u00A0Service carefully.'],
      ['comma', 'Read the Terms of, Service carefully.'],
    ]
    for (const [label, text] of cases) {
      it(label, () => {
        const ranges = termRanges(text, [tos], 'Nutzungsbedingungen lesen')
        expect(ranges.length).toBe(1)
        expect(normalizeTerm(matched(text, ranges)[0])).toBe('terms of service')
        expect(ranges[0].className).toBe(UNDERLINE_GREEN)
      })
    }

    it('matches a term whose stored form carries punctuation the tokenizer strips', () => {
      // Stored "node js" is what the DB can match against tokens of "Node.js".
      const ranges = termRanges('Install Node.js first.', [term({ source_term: 'node js', target_term: 'Node.js' })], 'Node.js installieren')
      expect(matched('Install Node.js first.', ranges)).toEqual(['Node.js'])
    })

    it('does not treat a hyphen as an arbitrary separator', () => {
      // The tokenizer preserves '-', so "two factor authentication" never
      // produces the token "two-factor authentication" — the client must not be
      // looser than the server either.
      const t = term({ source_term: 'two-factor authentication', target_term: 'Zwei-Faktor-Authentifizierung' })
      expect(termRanges('Enable two factor authentication now', [t], '')).toEqual([])
      expect(termRanges('Enable two-factor authentication now', [t], '').length).toBe(1)
    })
  })

  it('prefers the longest term when two overlap at the same position', () => {
    const text = 'Enter your email address here'
    const terms = [
      term({ id: 1, source_term: 'email', target_term: 'E-Mail' }),
      term({ id: 2, source_term: 'email address', target_term: 'E-Mail-Adresse' }),
    ]
    const ranges = termRanges(text, terms, 'E-Mail-Adresse eingeben')
    expect(matched(text, ranges)).toEqual(['email address'])
    expect(ranges[0].term.id).toBe(2)
  })

  it('keeps the highest-priority entry when two term bases define the same term', () => {
    const text = 'Enter your password'
    const ranges = termRanges(text, [
      term({ id: 1, target_term: 'Passwort', priority: 0 }),
      term({ id: 2, target_term: 'Kennwort', priority: 1 }),
    ], 'Passwort eingeben')
    expect(ranges[0].term.id).toBe(1)
  })

  describe('per-occurrence coloring', () => {
    it('colors the first covered occurrence green and the uncovered extra red', () => {
      const text = 'Enter your new password, repeat your password to verify.'
      const ranges = termRanges(text, [term()], 'Geben Sie Ihr neues Passwort ein, wiederholen Sie Ihr Passwortt.')
      expect(ranges.map(r => r.className)).toEqual([UNDERLINE_GREEN, UNDERLINE_RED])
    })

    it('drops a sub-word hit and does not let it consume a coverage slot', () => {
      // "passwords" is NOT a whole-word occurrence of "password" (trailing 's'),
      // so it is dropped; only the standalone "password" is underlined (green).
      const text = 'passwords: enter your password'
      const ranges = termRanges(text, [term()], 'Passwort eingeben')
      expect(matched(text, ranges)).toEqual(['password'])
      expect(ranges.map(r => r.className)).toEqual([UNDERLINE_GREEN])
    })

    it('reds an approved term that is missing from the target', () => {
      const ranges = termRanges('Enter your password', [term()], 'Etwas eingeben')
      expect(ranges[0].className).toBe(UNDERLINE_RED)
    })

    it('stays neutral for a pending term', () => {
      const ranges = termRanges('Enter your password', [term({ status: 'pending' })], 'Passwort eingeben')
      expect(ranges[0].className).toBe(UNDERLINE_NEUTRAL)
    })

    it('stays neutral with no target (read-only panes)', () => {
      const ranges = termRanges('Enter your password', [term()])
      expect(ranges[0].className).toBe(UNDERLINE_NEUTRAL)
    })
  })

  // The DB pre-filter is a loose substring ILIKE, so the server returns terms
  // that may only appear as SUB-words — notably single-letter terms ("A"→Ampere,
  // "s"→Sekunde) landing inside "Standard"/"system"/"ISO". A hit that isn't a
  // whole word is not a real occurrence and must not be underlined anywhere
  // (mirrors countWordOccurrences / the Go checker's isWordBoundary).
  describe('whole-word gate (sub-word hits are dropped, not underlined)', () => {
    it('drops single-letter matches that land inside larger words', () => {
      const a = term({ id: 1, source_term: 'a', target_term: 'Ampere' })
      const s = term({ id: 2, source_term: 's', target_term: 'Sekunde' })
      // No standalone 'a' or 's' here — every hit is inside ISO / Standard.
      const ranges = termRanges('ISO 9001 Standard', [a, s], 'ISO 9001 Norm')
      expect(ranges).toEqual([])
    })

    it('still matches a single-letter term when it stands alone as a word', () => {
      const a = term({ source_term: 'a', target_term: 'Ampere' })
      const ranges = termRanges('is a formal', [a], 'ist ein Ampere')
      expect(matched('is a formal', ranges)).toEqual(['a'])
    })

    it('drops sub-word hits in read-only panes too (no target)', () => {
      const s = term({ source_term: 's', target_term: 'Sekunde' })
      expect(termRanges('systems', [s])).toEqual([])
    })

    it('drops a sub-word hit for a pending term', () => {
      const p = term({ status: 'pending', source_term: 's', target_term: 'Sekunde' })
      expect(termRanges('systems', [p], 'Systeme')).toEqual([])
    })

    it("drops a single-letter match glued to a word by a possessive apostrophe", () => {
      // The tokenizer splits "company's" into "company" + "s", but that bare "s"
      // is a possessive fragment, not a standalone word — must not be underlined.
      const s = term({ source_term: 's', target_term: 'Sekunde' })
      expect(termRanges("a company's quality", [s], 'die Qualität')).toEqual([])
    })

    it('drops a match glued to a word by a contraction apostrophe', () => {
      const t = term({ source_term: 'don', target_term: 'X' })
      expect(termRanges("I don't know", [t], 'irgendwas')).toEqual([])
    })

    it('still matches a single-letter unit term when it stands alone', () => {
      const s = term({ source_term: 's', target_term: 'Sekunde' })
      expect(matched('wait 5 s now', termRanges('wait 5 s now', [s], 'warte 5 s'))).toEqual(['s'])
    })
  })

  describe('forbidden terms', () => {
    const banned = term({ status: 'forbidden', source_term: 'password', target_term: 'Kennwort' })

    it('reds the source when the banned translation is actually used', () => {
      const ranges = termRanges('Enter your password', [banned], 'Kennwort eingeben')
      expect(ranges[0].className).toBe(UNDERLINE_RED)
    })

    it('emits an undecorated range when the banned translation is absent', () => {
      // Nothing is wrong, so nothing should be marked — renderers skip ''.
      const ranges = termRanges('Enter your password', [banned], 'Passwort eingeben')
      expect(ranges[0].className).toBe('')
    })
  })

  // A concept whose SOURCE-language term is itself forbidden (a discouraged
  // input phrasing, e.g. "All rights are reserved" when the approved English is
  // "All rights reserved"). The returned status is the approved TARGET term's;
  // source_status carries the source term's own status. Since "forbidden"
  // constrains OUTPUT, it's inert on the unchangeable source: the span must show
  // as an informational (neutral) match, never red — even when the approved
  // target isn't present verbatim (which would normally paint red).
  describe('forbidden source-language term', () => {
    const forbiddenSource = term({
      source_term: 'all rights are reserved',
      target_term: 'Alle Rechte vorbehalten',
      status: 'approved',
      source_status: 'forbidden',
    })

    it('is neutral, never red, when the approved target is only paraphrased', () => {
      const ranges = termRanges('All rights are reserved', [forbiddenSource], 'Alle Rechte sind vorbehalten')
      expect(ranges).toHaveLength(1)
      expect(ranges[0].className).toBe(UNDERLINE_NEUTRAL)
    })

    it('is neutral even when the approved target is missing entirely', () => {
      const ranges = termRanges('All rights are reserved', [forbiddenSource], 'Etwas anderes')
      expect(ranges[0].className).toBe(UNDERLINE_NEUTRAL)
    })

    it('does not turn green when the approved target IS present verbatim', () => {
      // Still informational — we neither demand nor credit target usage for a
      // discouraged source phrasing.
      const ranges = termRanges('All rights are reserved', [forbiddenSource], 'Alle Rechte vorbehalten')
      expect(ranges[0].className).toBe(UNDERLINE_NEUTRAL)
    })
  })
})
