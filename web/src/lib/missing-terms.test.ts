import { describe, it, expect } from 'vitest'
import { getMissingTerms } from './missing-terms'
import { isTermApplied } from './cat-editor-logic'
import type { TermLookupResult } from '../api/terminology'

const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
  id: 1, concept_id: 1, source_term: 'password', target_term: 'Passwort', mt_translation: '',
  status: 'approved', notes: '', domain: '', term_base_id: 1,
  term_base_name: 'tb', priority: 0, ...over,
})

/** What F8 would insert for these terms, as `target@offset`. */
const missing = (source: string, terms: TermLookupResult[], target = '') =>
  getMissingTerms(terms, source, target).map(m => `${m.term.target_term}@${m.at}`)

describe('getMissingTerms', () => {
  it('offers the target term at the source term, in source order', () => {
    const terms = [
      term(),
      term({ id: 2, concept_id: 2, source_term: 'email address', target_term: 'E-Mail-Adresse' }),
    ]
    expect(missing('Enter your email address and password', terms)).toEqual(['E-Mail-Adresse@11', 'Passwort@29'])
  })

  it('skips a term the target already has, by the Terminology panel rule', () => {
    const src = 'Reset your password.'
    const tgt = 'Setzen Sie Ihr Passwort zurück.'
    expect(isTermApplied(src, tgt, 'password', 'Passwort')).toBe(true)
    expect(missing(src, [term()], tgt)).toEqual([])
  })

  it('never offers a forbidden term', () => {
    expect(missing('Enter your password', [term({ status: 'forbidden', target_term: 'Kennwort' })])).toEqual([])
  })

  it('offers one target per source word: the approved one over a pending synonym', () => {
    const terms = [
      term({ id: 1, status: 'pending', target_term: 'Kennwort' }),
      term({ id: 2, status: 'forbidden', target_term: 'PW' }),
      term({ id: 3, status: 'approved', target_term: 'Passwort' }),
    ]
    expect(missing('Enter your password', terms)).toEqual(['Passwort@11'])
  })

  it('offers a pending term when the source word has no approved one', () => {
    expect(missing('Enter your password', [term({ status: 'pending', target_term: 'Kennwort' })])).toEqual(['Kennwort@11'])
  })

  it('a synonym the target already uses counts as applied', () => {
    const terms = [
      term({ id: 1, target_term: 'Passwort' }),
      term({ id: 2, status: 'pending', target_term: 'Kennwort' }),
    ]
    expect(missing('Enter your password', terms, 'Kennwort eingeben')).toEqual([])
  })

  it('a term the source has twice and the target once is offered once more, at the later place', () => {
    const src = 'New password, repeat password'
    expect(missing(src, [term()], 'Neues Passwort')).toEqual(['Passwort@21'])
    expect(missing(src, [term()])).toEqual(['Passwort@4', 'Passwort@21'])
  })

  it('skips a term the source has only inside a longer word', () => {
    // The lookup's loose match returns "password" for "passwords".
    expect(missing('Manage passwords', [term()])).toEqual([])
  })

  it('returns nothing without terms', () => {
    expect(missing('Enter your password', [])).toEqual([])
  })
})

describe('getMissingTerms: synonyms across repeated occurrences', () => {
  it('offers nothing when the synonyms together cover every occurrence', () => {
    const terms = [term({ id: 1, target_term: 'Passwort' }), term({ id: 2, target_term: 'Kennwort' })]
    expect(missing('New password, repeat password', terms, 'Neues Passwort, Kennwort wiederholen')).toEqual([])
  })

  it('offers the rest when the synonyms cover only some occurrences', () => {
    const terms = [term({ id: 1, target_term: 'Passwort' }), term({ id: 2, target_term: 'Kennwort' })]
    expect(missing('password, password, password', terms, 'Passwort, Kennwort')).toEqual(['Passwort@20'])
  })
})

describe('getMissingTerms: a multi-word term the source spaces differently', () => {
  it('still offers it at its underlined place', () => {
    const terms = [term({ id: 2, concept_id: 2, source_term: 'email address', target_term: 'E-Mail-Adresse' })]
    expect(missing('Enter your email\u00a0address', terms)).toEqual(['E-Mail-Adresse@11'])
  })
})
