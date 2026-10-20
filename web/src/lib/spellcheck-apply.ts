import { applyPlainEdit } from './pm-offset'
import { hasXliffTags, tokenize } from './xliff-tiptap'
import type { SpellError } from './spellcheck-run'
import type { BatchDraftFix } from '../api/spellcheck'

/**
 * Rich-text edits are anchored on the WORD and its occurrence index, not on a
 * character offset.
 *
 * The tempting approach — reuse the LanguageTool offset against the rich string
 * — is wrong, because `active_text` and `active_rich_text` do not agree on what
 * the plain projection is. A placeholder round-trips as
 * `<ph id="4">{orderId}</ph>` in the rich form while the plain form keeps the
 * literal `{orderId}`; paired `bpt`/`ept` markup, by contrast, is dropped from
 * the plain form entirely. So the two strings drift by an amount that depends on
 * which tag kinds a segment happens to contain, and any offset carried from one
 * into the other lands in the wrong place.
 *
 * Occurrence anchoring sidesteps the question: we know the flagged word, and we
 * know it's the Nth occurrence in the plain text. Find the Nth occurrence among
 * the rich string's text runs and replace it there.
 */

/** Occurrences of `word` across a rich string's editable text runs only. */
export function richOccurrences(richText: string, word: string): number {
  let count = 0
  for (const token of tokenize(richText)) {
    if (token.type === 'text') count += countOccurrences(token.value ?? '', word)
  }
  return count
}

/** Count non-overlapping occurrences of `word` in `text`. */
export function countOccurrences(text: string, word: string): number {
  if (!word) return 0
  let count = 0
  let from = 0
  for (;;) {
    const idx = text.indexOf(word, from)
    if (idx < 0) return count
    count++
    from = idx + word.length
  }
}

/**
 * Replace the `occurrence`-th (0-based) instance of `word` in a rich string's
 * editable text, leaving all tag markup untouched.
 *
 * Returns null when the edit can't be made safely: the occurrence isn't there,
 * or the word straddles a tag boundary (in which case no single text run
 * contains it and splicing would delete markup).
 */
export function replaceOccurrenceInRich(
  richText: string,
  word: string,
  occurrence: number,
  replacement: string,
): string | null {
  if (!word) return null

  let seen = 0
  let out = ''
  let done = false

  for (const token of tokenize(richText)) {
    if (token.type !== 'text' || done) {
      out += token.type === 'text' ? (token.value ?? '') : token.raw
      continue
    }

    let value = token.value ?? ''
    let cursor = 0
    let rebuilt = ''
    for (;;) {
      const idx = value.indexOf(word, cursor)
      if (idx < 0) {
        rebuilt += value.slice(cursor)
        break
      }
      if (seen === occurrence) {
        rebuilt += value.slice(cursor, idx) + replacement
        cursor = idx + word.length
        seen++
        done = true
        rebuilt += value.slice(cursor)
        break
      }
      rebuilt += value.slice(cursor, idx + word.length)
      cursor = idx + word.length
      seen++
    }
    value = rebuilt
    out += value
  }

  return done ? out : null
}

/**
 * Turn spell check findings into bulk-draft payloads.
 *
 * Kept out of the page component because this is where a mistake corrupts a
 * segment: it does offset arithmetic, applies the same edit to both the plain
 * and the tagged form of the text, and has to get the ordering right when one
 * segment carries several findings.
 */

/** The subset of a translation row this module reads. */
export interface TranslationTextRow {
  active_text: string | null
  active_rich_text?: string
  updated_at?: string
}

export interface BuildFixesResult {
  fixes: BatchDraftFix[]
  /**
   * Findings that could not be turned into an edit — the flagged span straddles
   * an inline tag, or the text moved since the check. Surfaced rather than
   * dropped so the UI can say so instead of silently reporting success.
   */
  unfixable: SpellError[]
}

/**
 * Build one fix per translation, applying every listed finding for that row.
 *
 * Two ordering rules matter here:
 *
 *  1. Within a segment, findings are applied in DESCENDING offset order. Editing
 *     an earlier span shifts every later offset; going back-to-front leaves the
 *     remaining offsets valid without re-running the check.
 *  2. One fix per translation, carrying all of that row's replacements. Emitting
 *     two fixes for the same row would send the same `expected_updated_at`
 *     twice and the second would lose the optimistic-lock race against the
 *     first — so "Change all" would silently fix only one occurrence per
 *     segment.
 */
export function buildSpellFixes(
  errors: SpellError[],
  replacement: string,
  lookup: (translationId: number) => TranslationTextRow | undefined,
): BuildFixesResult {
  const byTranslation = new Map<number, SpellError[]>()
  for (const error of errors) {
    const list = byTranslation.get(error.translationId)
    if (list) list.push(error)
    else byTranslation.set(error.translationId, [error])
  }

  const fixes: BatchDraftFix[] = []
  const unfixable: SpellError[] = []

  for (const [translationId, list] of byTranslation) {
    const row = lookup(translationId)
    if (!row) {
      unfixable.push(...list)
      continue
    }

    const originalPlain = row.active_text ?? ''
    let plain = originalPlain
    // Only carry the rich form when it actually holds tag markup. For a
    // tag-free segment `active_rich_text` adds nothing, and rewriting it would
    // pin a redundant second copy of the text.
    const startRich = row.active_rich_text && hasXliffTags(row.active_rich_text)
      ? row.active_rich_text
      : null
    let rich = startRich
    let failed: SpellError | null = null

    // Offsets index `active_text`, so the plain edit uses them directly. The
    // rich edit uses the occurrence index computed from the ORIGINAL plain text
    // — see the note at the top of this file for why offsets don't transfer.
    const withOccurrence = list.map(error => ({
      error,
      occurrence: countOccurrences(originalPlain.slice(0, error.offset), error.word),
    }))

    for (const { error, occurrence } of [...withOccurrence].sort((a, b) => b.error.offset - a.error.offset)) {
      const nextPlain = applyPlainEdit(plain, error.offset, error.length, replacement)
      if (nextPlain === null) { failed = error; break }

      if (rich !== null) {
        // Guard against the word also appearing inside dropped tag markup,
        // which would make the occurrence indices disagree between the two
        // forms. Refusing beats writing into the wrong place.
        if (countOccurrences(originalPlain, error.word) !== richOccurrences(startRich!, error.word)) {
          failed = error
          break
        }
        const nextRich = replaceOccurrenceInRich(rich, error.word, occurrence, replacement)
        // Refusing beats guessing: splicing across a tag chip would delete the
        // markup, and a lost tag fails export later, far from this code.
        if (nextRich === null) { failed = error; break }
        rich = nextRich
      }
      plain = nextPlain
    }

    if (failed) {
      unfixable.push(...list)
      continue
    }

    fixes.push({
      translation_id: translationId,
      human_text: plain,
      human_rich_text: rich ?? undefined,
      expected_updated_at: row.updated_at ?? null,
    })
  }

  return { fixes, unfixable }
}
