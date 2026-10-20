import { describe, it, expect } from 'vitest'
import {
  findTermInText,
  findBestMatch,
  replaceTermInText,
  insertAtCursor,
  smartReplace,
  getTermSelection,
} from './term-insert'

describe('findTermInText', () => {
  it('finds exact match', () => {
    expect(findTermInText('Reset your password.', 'password')).toEqual({ index: 11, length: 8 })
  })

  it('finds case-insensitive match', () => {
    expect(findTermInText('Reset your Password.', 'password')).toEqual({ index: 11, length: 8 })
  })

  it('finds match with different case in search', () => {
    expect(findTermInText('reset your passwort zurück', 'Passwort')).toEqual({ index: 11, length: 8 })
  })

  it('returns null when not found', () => {
    expect(findTermInText('Hello world', 'password')).toBeNull()
  })

  it('returns first occurrence', () => {
    expect(findTermInText('password and password again', 'password')).toEqual({ index: 0, length: 8 })
  })

  it('handles empty text', () => {
    expect(findTermInText('', 'password')).toBeNull()
  })

  it('handles empty search term', () => {
    // Empty term returns null (guard against accidental matches)
    expect(findTermInText('hello', '')).toBeNull()
  })

  it('whole-word: rejects partial word match', () => {
    // "ihr" inside "Ihre" — not a whole word
    expect(findTermInText('Wir schätzen Ihre Geduld.', 'ihr', true)).toBeNull()
  })

  it('whole-word: accepts word boundary match', () => {
    // "Ihr" as a standalone word
    expect(findTermInText('Setzen Sie Ihr Passwort zurück.', 'Ihr', true))
      .toEqual({ index: 11, length: 3 })
  })

  it('whole-word: handles punctuation as boundary', () => {
    // "Passwort" followed by period
    expect(findTermInText('Ihr Passwort.', 'Passwort', true))
      .toEqual({ index: 4, length: 8 })
  })

  it('whole-word: rejects match inside longer word', () => {
    // "Passwort" inside "Passwortas" — not a whole word
    expect(findTermInText('Setzen Sie Ihr Passwortas zurück.', 'Passwort', true)).toBeNull()
  })

  it('whole-word: skips partial match and finds later whole match', () => {
    // First "pass" is inside "password", second is standalone
    expect(findTermInText('Enter password or pass code', 'pass', true))
      .toEqual({ index: 18, length: 4 })
  })

  it('whole-word: handles CJK correctly (no space-separated words)', () => {
    // CJK characters are not "word chars" in our definition, so boundaries always match
    expect(findTermInText('パスワードを入力してください', 'パスワード', true))
      .toEqual({ index: 0, length: 5 })
  })

  it('whole-word: handles accented characters as word chars', () => {
    // "Über" should NOT match inside "Übersicht"
    expect(findTermInText('die Übersicht', 'Über', true)).toBeNull()
    // But standalone "Über" should match
    expect(findTermInText('Über den Berg', 'Über', true))
      .toEqual({ index: 0, length: 4 })
  })

  it('whole-word: handles hyphenated terms', () => {
    // Hyphen is a word boundary — "E-Mail" matches inside "E-Mail-Adresse"
    expect(findTermInText('Ihre E-Mail-Adresse', 'E-Mail', true))
      .toEqual({ index: 5, length: 6 })
  })

  it('finds multi-word terms', () => {
    expect(findTermInText('Enter your email address here', 'email address')).toEqual({ index: 11, length: 13 })
  })

  it('finds term at start of text', () => {
    expect(findTermInText('password reset', 'password')).toEqual({ index: 0, length: 8 })
  })

  it('finds term at end of text', () => {
    expect(findTermInText('reset password', 'password')).toEqual({ index: 6, length: 8 })
  })

  // 6.4: Turkish İ/i case folding
  it('handles Turkish İ case-insensitively', () => {
    // Turkish dotted İ should match lowercase i
    expect(findTermInText('istanbul is a city', 'İstanbul')).toEqual({ index: 0, length: 8 })
  })
})

describe('findBestMatch', () => {
  it('finds exact source term match first', () => {
    // "password" is literally in the translation
    expect(findBestMatch('enter your password here', 'password', 'Passwort')).toEqual({ index: 11, length: 8 })
  })

  it('returns null when source not found and no MT translation', () => {
    // Source "password" NOT in German translation, no MT translation provided
    expect(findBestMatch('Setzen Sie Ihr Passwort zurück.', 'password', 'Passwortas')).toBeNull()
  })

  it('finds MT translation match when source not found', () => {
    // Source "data" not in text, but MT translation "Daten" IS in text
    const match = findBestMatch('Ihre Daten sind sicher', 'data', 'Datensatz', 'Daten')
    expect(match).toEqual({ index: 5, length: 5 })
  })

  it('returns null when nothing matches', () => {
    expect(findBestMatch('Hallo Welt', 'password', 'Kennwort')).toBeNull()
  })

  it('respects minimum match length for MT translation', () => {
    // MT translation "ab" (2 chars) — below default min of 3
    expect(findBestMatch('xy ab z', 'notfound', 'abc', 'ab')).toBeNull()
    // With min=2 it should match
    expect(findBestMatch('xy ab z', 'notfound', 'abc', 'ab', 2)).toEqual({ index: 3, length: 2 })
  })

  it('prefers source match over MT translation', () => {
    // Source "pass" IS in text — should use source match, not MT
    const match = findBestMatch('reset your pass here', 'pass', 'Passwort', 'Kennwort')
    expect(match).toEqual({ index: 11, length: 4 }) // source exact match wins
  })

  it('MT translation ignored when empty', () => {
    const match = findBestMatch('some text here', 'notfound', 'alsonotfound', '')
    expect(match).toBeNull()
  })

  it('handles French: mot de passe → code secret', () => {
    // Source "mot de passe" not in English, no MT translation
    expect(findBestMatch('password reset', 'mot de passe', 'code secret')).toBeNull()
    // But in French text it matches via source
    expect(findBestMatch('Réinitialisez votre mot de passe', 'mot de passe', 'code secret')).toEqual({ index: 20, length: 12 })
  })

  it('MT translation finds synonym the term base does not anticipate', () => {
    // Term base: data → Datensatz, but MT used "Daten" (different word, same root)
    // MT-translating "data" with the same engine gives "Daten" — matches
    const match = findBestMatch('Schützen Sie Ihre Daten', 'data', 'Datensatz', 'Daten')
    expect(match).toEqual({ index: 18, length: 5 })
  })

  it('MT translation is case-insensitive', () => {
    const match = findBestMatch('ihre daten sind sicher', 'data', 'Datensatz', 'Daten')
    expect(match).toEqual({ index: 5, length: 5 })
  })

  it('splits multi-word MT when source is single word', () => {
    // MT translated "password" as "passwort vergessen" (2 words for 1 source word)
    // Should match only "Passwort" (longest individual word), not "Passwort vergessen"
    const match = findBestMatch(
      "Klicken Sie auf den Link 'Passwort vergessen?'",
      'password', 'Passwortas', 'passwort vergessen'
    )
    expect(match).not.toBeNull()
    expect(match!.length).toBe(8) // "Passwort" not "Passwort vergessen" (18)
  })

  it('uses full MT when word counts match source', () => {
    // Source "email address" (2 words), MT "E-Mail-Adresse" (1 word) — fewer words, use full
    const match = findBestMatch(
      'Geben Sie Ihre E-Mail-Adresse ein',
      'email address', 'E-Mail-Adresse', 'E-Mail-Adresse'
    )
    expect(match).toEqual({ index: 15, length: 14 })
  })

  it('uses full MT when same word count as source', () => {
    // Source "credit card" (2 words), MT "carte crédit" (2 words) — same count, use full
    const match = findBestMatch(
      'Votre carte crédit est expirée',
      'credit card', 'carte de crédit', 'carte crédit'
    )
    expect(match).toEqual({ index: 6, length: 12 })
  })

  it('splits multi-word MT and tries words in original order', () => {
    // MT returned "der Datensatz" (2 words) for source "data" (1 word)
    // "der" (3 chars) is tried first but not in text → "Datensatz" matches
    const match = findBestMatch(
      'Ihr Datensatz ist sicher', 'data', 'Datensatz', 'der Datensatz'
    )
    expect(match).toEqual({ index: 4, length: 9 }) // "Datensatz"
  })

  it('rejects partial word match via MT (whole-word boundary)', () => {
    // MT translated "your" as "ihr", but "ihr" is inside "Ihre" (not a whole word)
    // Should NOT match — prevents highlighting "Ih" or partial words
    const match = findBestMatch(
      'Wir schätzen Ihre Geduld.', 'your', 'tuo', 'ihr'
    )
    expect(match).toBeNull()
  })

  it('accepts whole-word MT match', () => {
    // "ihr" as a standalone word in "Setzen Sie Ihr Passwort"
    const match = findBestMatch(
      'Setzen Sie Ihr Passwort zurück.', 'your', 'tuo', 'ihr'
    )
    expect(match).toEqual({ index: 11, length: 3 })
  })

  it('splits multi-word MT and skips short words below minMatchLength', () => {
    // MT "zu Passwort" — "zu" is 2 chars, below default min 3
    const match = findBestMatch(
      'Setzen Sie Ihr Passwort zurück.', 'password', 'Passwortas', 'zu Passwort'
    )
    expect(match).toEqual({ index: 15, length: 8 }) // "Passwort", skipped "zu"
  })
})

describe('replaceTermInText', () => {
  it('replaces source term with target term', () => {
    const result = replaceTermInText('Setzen Sie Ihr Passwort zurück.', 'Passwort', 'Kennwort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Setzen Sie Ihr Kennwort zurück.')
    expect(result!.cursor).toEqual({ start: 23, end: 23 }) // after "Kennwort"
  })

  it('replaces case-insensitively', () => {
    const result = replaceTermInText('Ihr passwort zurück', 'Passwort', 'Kennwort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Ihr Kennwort zurück')
  })

  it('returns null when source not found', () => {
    expect(replaceTermInText('Hello world', 'password', 'Passwort')).toBeNull()
  })

  it('handles replacement with longer term', () => {
    const result = replaceTermInText('das Passwort', 'Passwort', 'Sicherheitspasswort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('das Sicherheitspasswort')
    expect(result!.cursor.start).toBe(23)
  })

  it('handles replacement with shorter term', () => {
    const result = replaceTermInText('das Sicherheitspasswort', 'Sicherheitspasswort', 'PW')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('das PW')
  })

  it('handles replacement with empty target', () => {
    const result = replaceTermInText('remove password here', 'password', '')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('remove  here')
  })

  it('replaces only the first occurrence', () => {
    const result = replaceTermInText('Passwort und Passwort', 'Passwort', 'Kennwort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Kennwort und Passwort')
  })

  it('handles term at start', () => {
    const result = replaceTermInText('Passwort zurücksetzen', 'Passwort', 'Kennwort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Kennwort zurücksetzen')
  })

  it('handles term at end', () => {
    const result = replaceTermInText('Geben Sie Ihr Passwort', 'Passwort', 'Kennwort')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Geben Sie Ihr Kennwort')
  })
})

describe('insertAtCursor', () => {
  it('inserts at beginning when cursor is at 0', () => {
    const result = insertAtCursor('world', { start: 0, end: 0 }, 'hello ')
    expect(result.newText).toBe('hello world')
    expect(result.cursor).toEqual({ start: 6, end: 6 })
  })

  it('inserts at end', () => {
    const result = insertAtCursor('hello', { start: 5, end: 5 }, ' world')
    expect(result.newText).toBe('hello world')
    expect(result.cursor).toEqual({ start: 11, end: 11 })
  })

  it('inserts in middle', () => {
    const result = insertAtCursor('hello world', { start: 5, end: 5 }, ' beautiful')
    expect(result.newText).toBe('hello beautiful world')
    expect(result.cursor).toEqual({ start: 15, end: 15 })
  })

  it('replaces selected text', () => {
    const result = insertAtCursor('hello world', { start: 6, end: 11 }, 'everyone')
    expect(result.newText).toBe('hello everyone')
    expect(result.cursor).toEqual({ start: 14, end: 14 })
  })

  it('replaces all text when everything is selected', () => {
    const result = insertAtCursor('old text', { start: 0, end: 8 }, 'new text')
    expect(result.newText).toBe('new text')
  })

  it('handles empty text with insert', () => {
    const result = insertAtCursor('', { start: 0, end: 0 }, 'Kennwort')
    expect(result.newText).toBe('Kennwort')
    expect(result.cursor).toEqual({ start: 8, end: 8 })
  })
})

describe('smartReplace', () => {
  it('replaces source term when found in text', () => {
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'Passwort',
      'Kennwort',
      { start: 0, end: 0 }
    )
    expect(result.newText).toBe('Setzen Sie Ihr Kennwort zurück.')
  })

  it('falls back to cursor insert when neither source nor MT matches', () => {
    const result = smartReplace(
      'Hallo Welt',
      'password',
      'Kennwort',
      { start: 5, end: 5 }
    )
    expect(result.newText).toBe('HalloKennwort Welt')
  })

  it('replaces selection when text is manually selected', () => {
    const result = smartReplace(
      'Hallo Welt',
      'password',
      'Kennwort',
      { start: 6, end: 10 } // "Welt" selected
    )
    expect(result.newText).toBe('Hallo Kennwort')
  })

  it('prefers source term replacement over cursor position', () => {
    // Even if cursor is at a different position, find-and-replace wins
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'Passwort',
      'Kennwort',
      { start: 0, end: 0 } // cursor at start, but Passwort is at index 15
    )
    expect(result.newText).toBe('Setzen Sie Ihr Kennwort zurück.')
    expect(result.cursor.start).toBe(23) // after "Kennwort"
  })

  it('handles case where source term appears in different case', () => {
    const result = smartReplace(
      'geben sie ihr passwort ein',
      'Passwort',
      'Kennwort',
      { start: 0, end: 0 }
    )
    expect(result.newText).toBe('geben sie ihr Kennwort ein')
  })

  it('replaces MT translation match when source not found', () => {
    // Source "data" not in text, but MT translation "Daten" IS in text
    const result = smartReplace(
      'Ihre Daten sind sicher',
      'data',
      'Datensatz',
      { start: 0, end: 0 },
      'Daten'
    )
    expect(result.newText).toBe('Ihre Datensatz sind sicher')
  })

  it('falls back to cursor insert when source and MT both miss', () => {
    const result = smartReplace(
      'Hallo Welt',
      'password',
      'Kennwort',
      { start: 10, end: 10 },
      'Passwort' // MT translation also not in text
    )
    expect(result.newText).toBe('Hallo WeltKennwort')
  })

  // When a term is partially applied, click must skip the occurrence
  // that's already correct and land on the leftover, not overwrite the correct one.
  it('skips the already-correct occurrence and replaces the leftover source term', () => {
    // First "Passwort" is correct; the second occurrence is still English "password".
    const result = smartReplace(
      'Passwort eingeben, dann password erneut eingeben.',
      'password',
      'Passwort',
      { start: 0, end: 0 },
    )
    expect(result.newText).toBe('Passwort eingeben, dann Passwort erneut eingeben.')
  })

  it('does not overwrite an already-correct occurrence — falls back to cursor insert', () => {
    // Only the correct "Passwort" is findable (the other spot has a typo the tool
    // cannot locate); the MT rendering equals the target term, so every candidate
    // is already-correct and gets skipped → insert at the caret instead.
    const result = smartReplace(
      'Passwort ok, Passwortt falsch.',
      'password',
      'Passwort',
      { start: 30, end: 30 },
      'Passwort',
    )
    expect(result.newText).toBe('Passwort ok, Passwortt falsch.Passwort')
  })
})

describe('getTermSelection', () => {
  it('returns selection range for matching term', () => {
    expect(getTermSelection('Setzen Sie Ihr Passwort zurück.', 'Passwort'))
      .toEqual({ start: 15, end: 23 })
  })

  it('returns selection range case-insensitively', () => {
    expect(getTermSelection('ihr passwort', 'Passwort'))
      .toEqual({ start: 4, end: 12 })
  })

  it('returns null when term not found', () => {
    expect(getTermSelection('Hello world', 'password')).toBeNull()
  })

  it('returns selection for term at start', () => {
    expect(getTermSelection('Passwort eingeben', 'Passwort'))
      .toEqual({ start: 0, end: 8 })
  })

  it('returns selection for multi-word term', () => {
    expect(getTermSelection('Geben Sie Ihre E-Mail-Adresse ein', 'E-Mail-Adresse'))
      .toEqual({ start: 15, end: 29 })
  })

  it('handles empty text', () => {
    expect(getTermSelection('', 'password')).toBeNull()
  })

  it('returns null when source not found and no MT (no prefix matching)', () => {
    // Pins: no prefix matching
    expect(getTermSelection('Setzen Sie Ihr Passwort zurück', 'password', 'Passwortas')).toBeNull()
  })

  it('selects via MT translation when source not found', () => {
    // Source "data" not in text, but MT translation "Daten" IS
    expect(getTermSelection('Ihre Daten sind sicher', 'data', 'Datensatz', 'Daten'))
      .toEqual({ start: 5, end: 10 })
  })

  it('works without targetTerm (backward compatible)', () => {
    expect(getTermSelection('enter password', 'password')).toEqual({ start: 6, end: 14 })
  })
})

describe('edge cases: real-world terminology scenarios', () => {
  it('same-language: replaces exact source match', () => {
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'Passwort',
      'Passwortas',
      { start: 31, end: 31 }
    )
    expect(result.newText).toBe('Setzen Sie Ihr Passwortas zurück.')
  })

  it('cross-language with MT: finds MT-translated term', () => {
    // Source "password" not in German text, MT translation "Passwort" IS in text
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'password',
      'Passwortas',
      { start: 31, end: 31 },
      'Passwort' // MT translated "password" → "Passwort"
    )
    expect(result.newText).toBe('Setzen Sie Ihr Passwortas zurück.')
  })

  it('cross-language without MT: inserts at cursor', () => {
    // Source "password" not in German text, no MT translation
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'password',
      'Passwortas',
      { start: 31, end: 31 }
    )
    // No match found — falls back to cursor insert at end
    expect(result.newText).toBe('Setzen Sie Ihr Passwort zurück.Passwortas')
  })

  it('Spanish: replaces contraseña with term base term', () => {
    const result = smartReplace(
      'Restablezca su contraseña.',
      'contraseña',
      'clave de acceso',
      { start: 0, end: 0 }
    )
    expect(result.newText).toBe('Restablezca su clave de acceso.')
  })

  it('clicking again after replacement via source — inserts at cursor (no more false prefix match)', () => {
    // After first click: "Passwortas" is now in the text, "Passwort" (source) no longer present
    // Second click: "password" not found, no MT → falls back to cursor insert
    const result = smartReplace(
      'Setzen Sie Ihr Passwortas zurück.',
      'password',
      'Passwortas',
      { start: 25, end: 25 }
    )
    // Pins: no prefix matching, so this falls back to the cursor
    expect(result.newText).toBe('Setzen Sie Ihr PasswortasPasswortas zurück.')
  })

  it('clicking again after replacement via MT — whole-word matching prevents partial match', () => {
    // After first click with MT: "Passwortas" is now in the text
    // "Passwort" (MT translation) is a substring of "Passwortas" but NOT a whole-word match
    // (the 'a' after "Passwort" is a Latin letter → not a word boundary)
    // So findBestMatch correctly returns null → falls back to cursor insert
    const result = smartReplace(
      'Setzen Sie Ihr Passwortas zurück.',
      'password',
      'Passwortas',
      { start: 25, end: 25 },
      'Passwort'
    )
    // No whole-word match found — falls back to inserting at cursor position 25
    expect(result.newText).toBe('Setzen Sie Ihr PasswortasPasswortas zurück.')
  })

  it('handles partial word match correctly (substring issue)', () => {
    const result = findTermInText('Reset your password', 'pass')
    expect(result).not.toBeNull()
    expect(result!.index).toBe(11)
  })

  it('multiple term base terms in one segment: sequential replacement', () => {
    const text = 'Enter your email address and password'
    const first = smartReplace(text, 'email address', 'E-Mail-Adresse', { start: 0, end: 0 })
    expect(first.newText).toBe('Enter your E-Mail-Adresse and password')
    const second = smartReplace(first.newText, 'password', 'Passwort', first.cursor)
    expect(second.newText).toBe('Enter your E-Mail-Adresse and Passwort')
  })

  it('cross-language sequential with MT: DE translation with multiple terms', () => {
    // German translation with two terminology-matchable words via MT translations
    const text = 'Geben Sie Ihre E-Mail-Adresse und Passwort ein'
    const result = smartReplace(text, 'password', 'Passwortas', { start: 0, end: 0 }, 'Passwort')
    expect(result.newText).toBe('Geben Sie Ihre E-Mail-Adresse und Passwortas ein')
  })

  it('replaces with selection when text is manually selected', () => {
    const result = smartReplace(
      'Setzen Sie Ihr Passwort zurück.',
      'password',
      'Passwortas',
      { start: 15, end: 23 } // "Passwort" manually selected
    )
    expect(result.newText).toBe('Setzen Sie Ihr Passwortas zurück.')
  })

  it('unicode text: handles accented characters', () => {
    const result = replaceTermInText('Réinitialisez votre mot de passe', 'mot de passe', 'code secret')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('Réinitialisez votre code secret')
  })

  it('CJK text: handles Japanese/Chinese characters', () => {
    const result = replaceTermInText('パスワードを入力してください', 'パスワード', '暗証番号')
    expect(result).not.toBeNull()
    expect(result!.newText).toBe('暗証番号を入力してください')
  })
})
