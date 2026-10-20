/**
 * Term insertion logic for the CAT editor.
 * All functions are pure (operate on strings/positions).
 */

export interface CursorPos {
  start: number
  end: number
}

/**
 * Check if a character belongs to a space-separated script (Latin, Cyrillic, Greek + digits).
 * CJK and other scripts are NOT considered "word characters" because they don't use
 * spaces between words — every character boundary is effectively a word boundary.
 */
function isWordChar(ch: string): boolean {
  return /[\p{Script=Latin}\p{Script=Cyrillic}\p{Script=Greek}\p{N}]/u.test(ch)
}

// NFKD normalization + strip combining marks for locale-safe case folding
// (handles Turkish İ→i, accented characters, etc.). Shared by all matchers.
const caseFold = (s: string) => s.normalize('NFKD').replace(/\p{M}/gu, '').toLowerCase()

/**
 * Find the position of a term (case-insensitive) in the text.
 * Returns the index and length, or null if not found.
 *
 * When `wholeWord` is true, only matches at word boundaries — prevents
 * matching "ihr" inside "Ihre" or "pass" inside "password". Word boundaries
 * are checked using Unicode script awareness: Latin/Cyrillic/Greek characters
 * form word continuations, while CJK characters are always valid boundaries
 * (since CJK scripts don't use spaces between words).
 */
export function findTermInText(
  text: string,
  term: string,
  wholeWord = false
): { index: number; length: number } | null {
  if (!term) return null
  const textLower = caseFold(text)
  const termLower = caseFold(term)
  let startPos = 0

  while (startPos < textLower.length) {
    const idx = textLower.indexOf(termLower, startPos)
    if (idx === -1) return null

    if (!wholeWord) {
      return { index: idx, length: term.length }
    }

    // Check word boundaries: char before and after the match must not be word characters
    const beforeOk = idx === 0 || !isWordChar(text[idx - 1])
    const afterOk = (idx + term.length) >= text.length || !isWordChar(text[idx + term.length])

    if (beforeOk && afterOk) {
      return { index: idx, length: term.length }
    }

    // Not a whole-word match — try next occurrence
    startPos = idx + 1
  }

  return null
}

/**
 * All (case-insensitive) matches of `term` in `text`, in order. With
 * `wholeWord`, only word-boundary matches are returned. Mirrors findTermInText's
 * matching so callers can iterate occurrences (e.g. to skip ones that are
 * already correctly translated).
 */
export function findAllTermMatches(
  text: string,
  term: string,
  wholeWord = false
): { index: number; length: number }[] {
  if (!term) return []
  const textLower = caseFold(text)
  const termLower = caseFold(term)
  const out: { index: number; length: number }[] = []
  let startPos = 0
  while (startPos <= textLower.length) {
    const idx = textLower.indexOf(termLower, startPos)
    if (idx === -1) break
    const beforeOk = idx === 0 || !isWordChar(text[idx - 1])
    const afterOk = (idx + term.length) >= text.length || !isWordChar(text[idx + term.length])
    if (!wholeWord || (beforeOk && afterOk)) {
      out.push({ index: idx, length: term.length })
    }
    startPos = idx + 1
  }
  return out
}

/**
 * Find the best match for a term base term in the translation text.
 * Tries multiple strategies in order:
 * 1. Exact source term match (works for loanwords, same-language text)
 * 2. MT translation match — finds what the MT engine actually produced for this term
 * Returns null if nothing matches.
 *
 * No prefix matching: a target-term prefix hits different words ("Datensatz"
 * vs "Daten"). MT-assisted matching (Strategy 2) covers inflected forms instead.
 *
 * `skipAlreadyCorrect` (used by the click/replace path) skips any candidate whose
 * matched text is ALREADY exactly the target term, so clicking never overwrites
 * an occurrence that's already correctly translated. It is
 * OFF by default: the hover-preview / auto-select callers (getTermSelection)
 * want to highlight the term even when it's already correct.
 */
export function findBestMatch(
  text: string,
  sourceTerm: string,
  targetTerm: string,
  mtTranslation?: string,
  minMatchLength = 3,
  skipAlreadyCorrect = false,
): { index: number; length: number } | null {
  // All matching in findBestMatch uses whole-word boundaries to avoid
  // highlighting partial words (e.g. "ihr" inside "Ihre", "pass" inside "password").
  // Word boundaries are Unicode-aware: Latin/Cyrillic/Greek letters form word continuations,
  // while CJK characters are always valid boundaries (no spaces between CJK words).

  const target = caseFold(targetTerm ?? '')
  const replaceable = (m: { index: number; length: number }) =>
    !skipAlreadyCorrect || target === '' || caseFold(text.slice(m.index, m.index + m.length)) !== target

  // Strategy 1: exact source term in translation (loanwords, same-language)
  const exactSource = findAllTermMatches(text, sourceTerm, true).find(replaceable)
  if (exactSource) return exactSource

  // Strategy 2: MT translation match — if the term base source term was pre-translated with MT,
  // search for that MT translation in the text. This catches cases where MT used a synonym
  // (e.g. term base: data→Datensatz, MT used "Daten" in segment,
  // MT-translating "data" also gives "Daten" → found and replaceable)
  //
  // Guard against over-matching: MT engines sometimes return more words than the source
  // (e.g. "password" → "passwort vergessen"). When the MT translation has more words than
  // the source term, try matching individual MT words in original order instead of the full
  // phrase, to avoid highlighting unrelated adjacent text.
  if (mtTranslation && mtTranslation.length >= minMatchLength) {
    const sourceWordCount = sourceTerm.trim().split(/\s+/).length
    const mtWords = mtTranslation.trim().split(/\s+/)

    if (mtWords.length > sourceWordCount) {
      // MT returned more words than source — try individual words in original order
      // (MT engines typically produce the main/content word first)
      for (const word of mtWords) {
        if (word.length >= minMatchLength) {
          const match = findAllTermMatches(text, word, true).find(replaceable)
          if (match) return match
        }
      }
    } else {
      // Word counts match — use full MT translation
      const mtMatch = findAllTermMatches(text, mtTranslation, true).find(replaceable)
      if (mtMatch) return mtMatch
    }
  }

  return null
}

/**
 * Replace a matched term in the text with a target term.
 * If the source term is found (case-insensitive), replaces the first occurrence.
 * Returns the new text and new cursor position, or null if source not found.
 */
export function replaceTermInText(
  text: string,
  sourceTerm: string,
  targetTerm: string
): { newText: string; cursor: CursorPos } | null {
  const match = findTermInText(text, sourceTerm)
  if (!match) return null
  const newText = text.substring(0, match.index) + targetTerm + text.substring(match.index + match.length)
  const newCursorPos = match.index + targetTerm.length
  return { newText, cursor: { start: newCursorPos, end: newCursorPos } }
}

/**
 * Insert text at a cursor position (replaces selected range if start !== end).
 * Returns the new text and new cursor position.
 */
export function insertAtCursor(
  text: string,
  cursor: CursorPos,
  insertText: string
): { newText: string; cursor: CursorPos } {
  const newText = text.substring(0, cursor.start) + insertText + text.substring(cursor.end)
  const newCursorPos = cursor.start + insertText.length
  return { newText, cursor: { start: newCursorPos, end: newCursorPos } }
}

/**
 * Smart replace: tries to find the best match in the text and replace it.
 * Strategy order:
 * 1. If text is selected (cursor.start !== cursor.end) → replace selection
 * 2. Find a source term / MT rendering that is NOT already the correct target
 *    term → replace it (skips occurrences already correctly translated)
 * 3. Fall back to insert at cursor
 */
export function smartReplace(
  text: string,
  sourceTerm: string,
  targetTerm: string,
  cursor: CursorPos,
  mtTranslation?: string
): { newText: string; cursor: CursorPos } {
  // If text is selected, always replace selection
  if (cursor.start !== cursor.end) {
    return insertAtCursor(text, cursor, targetTerm)
  }

  // Find the best replaceable match — skipping any occurrence that already
  // carries the correct target term, so a partially-applied term lands on the
  // wrong/untranslated spot instead of overwriting a correct one.
  const match = findBestMatch(text, sourceTerm, targetTerm, mtTranslation, 3, true)
  if (match) {
    const newText = text.substring(0, match.index) + targetTerm + text.substring(match.index + match.length)
    const newCursorPos = match.index + targetTerm.length
    return { newText, cursor: { start: newCursorPos, end: newCursorPos } }
  }

  // Nothing matched — insert at cursor
  return insertAtCursor(text, cursor, targetTerm)
}

/**
 * Get the selection range for a term in the text.
 * Used for auto-select (single term) and hover-to-select (multiple terms).
 * Tries source term first, then MT translation match.
 * Returns the cursor range to select, or null if not found.
 */
export function getTermSelection(
  text: string,
  sourceTerm: string,
  targetTerm?: string,
  mtTranslation?: string,
): CursorPos | null {
  const match = findBestMatch(text, sourceTerm, targetTerm ?? sourceTerm, mtTranslation)
  if (!match) return null
  return { start: match.index, end: match.index + match.length }
}
