import { describe, it, expect } from 'vitest'
import { isTermApplied } from './cat-editor-logic'

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
