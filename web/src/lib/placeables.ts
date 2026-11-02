import type { Placeable } from '../types/placeable'
import type { DecoratedRange } from './text-ranges'

export type { Placeable }

/** The dotted underline a placeable gets in the source. A border, so it
 *  composes with term underlines (text-decoration) and diff tints (background). */
const PLACEABLE_CLASS = 'border-b-2 border-dotted border-sky-500 dark:border-sky-400'

const KIND_LABELS: Record<string, string> = {
  number: 'Number',
  date: 'Date',
  time: 'Time',
  measurement: 'Measurement',
  currency: 'Amount',
  url: 'Web address',
  email: 'Email address',
  variable: 'Variable',
  code: 'Code',
}

/** What a placeable is, in words. */
export function placeableLabel(p: Placeable): string {
  return KIND_LABELS[p.kind] ?? 'Placeable'
}

/**
 * Where the server's placeables sit in `flat`, the text the reader sees. The
 * server sends them in text order but without offsets (its text is the plain
 * source, without chips), so each is found by scanning forward from the end
 * of the previous one. One that is not found is skipped.
 */
export function placeableRanges(flat: string, placeables: readonly Placeable[]): DecoratedRange[] {
  const out: DecoratedRange[] = []
  let from = 0
  for (const p of placeables) {
    if (!p.text) continue
    const at = flat.indexOf(p.text, from)
    if (at < 0) continue
    out.push({
      start: at,
      end: at + p.text.length,
      className: PLACEABLE_CLASS,
      title: `${placeableLabel(p)} (F8 inserts the next missing one)`,
      insertText: p.text,
    })
    from = at + p.text.length
  }
  return out
}

// A letter or digit that makes a match part of a longer word. Chinese,
// Japanese, Korean, Thai, Lao, Khmer and Burmese write a number right against
// its word ("500円", "500บาท"), so their letters do not count; the server's
// placeable.IsWordLetter draws the same line.
const WORD = /\p{Nd}|(?![\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}\p{Script=Thai}\p{Script=Lao}\p{Script=Khmer}\p{Script=Myanmar}])\p{L}/u

const CODE_CHAR = /[\p{Script=Latin}\p{Script=Greek}\p{Script=Cyrillic}\p{Nd}]/u

/** Whether the text on one side of a match continues it: `next` is the
 *  character right against the match, `then` the one beyond it. */
function runsOn(kind: string, next: string, then: string): boolean {
  if (next === '') return false
  // A code is made of Latin, Greek and Cyrillic letters and digits (the
  // server's codeToken), and runs on only into what it could have been read as.
  const word = kind === 'code' ? CODE_CHAR : WORD
  if (word.test(next)) return true
  // A code runs on through "_", "." or "/" and a code character, or through
  // "-" and a digit ("XJ-500B-2"); "-" and a word is a compound ("MP3-Player").
  if (kind !== 'code' || then === '') return false
  if ('_./'.includes(next)) return CODE_CHAR.test(then)
  return next === '-' && /\p{Nd}/u.test(then)
}

/** Occurrences of a placeable in `hay`, the way internal/placeable `Count`
 *  counts them: a variable, web or email address wherever its text is ("%d"
 *  in "%dx%d"); a code where it does not run on into a longer code ("MP3" in
 *  "MP3-Player", not "XJ-500B" in "XJ-500B-2"); anything else where no word
 *  character is right against it ("5" is not in "15"). */
function countOccurrences(hay: string, p: Placeable): number {
  const needle = p.text
  const anywhere = p.kind === 'variable' || p.kind === 'url' || p.kind === 'email'
  let n = 0
  for (let at = hay.indexOf(needle); at >= 0; at = hay.indexOf(needle, at + 1)) {
    const end = at + needle.length
    if (!anywhere) {
      const before = [...hay.slice(0, at)].reverse()
      const after = [...hay.slice(end)]
      // Only a letter or digit edge of the placeable can run on: "$5" is in "PLUS$5".
      if (WORD.test(needle[0]) && runsOn(p.kind, before[0] ?? '', before[1] ?? '')) continue
      if (WORD.test(needle[needle.length - 1]) && runsOn(p.kind, after[0] ?? '', after[1] ?? '')) continue
    }
    n++
    at = end - 1
  }
  return n
}

/**
 * The source placeables the target does not have yet, in source order,
 * counted: a variable the source has twice must be in the target twice.
 */
export function getMissingPlaceables(placeables: readonly Placeable[], target: string): Placeable[] {
  const left = new Map<string, number>()
  const missing: Placeable[] = []
  for (const p of placeables) {
    if (!p.text) continue
    if (!left.has(p.text)) left.set(p.text, countOccurrences(target, p))
    const n = left.get(p.text)!
    if (n > 0) {
      left.set(p.text, n - 1)
      continue
    }
    missing.push(p)
  }
  return missing
}
