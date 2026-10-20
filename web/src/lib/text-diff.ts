/**
 * The diff core behind every read-only "what changed" highlight: the TM
 * fuzzy-match diff (lib/tm-diff.ts) and the history track changes
 * (lib/track-changes.ts). Both use `diff-match-patch` under one length cap,
 * and differ only in the unit they compare. Callers cache their own ranges
 * with `boundedCache`.
 */

import { diff_match_patch } from 'diff-match-patch'

export const DIFF_DELETE = -1
export const DIFF_INSERT = 1

/** `[op, text]`, in `diff-match-patch`'s order: walking the ops rebuilds
 *  `reference` from EQUAL + DELETE and `text` from EQUAL + INSERT. */
export type DiffOp = [op: number, text: string]

/** Mirrors MaxScoreLength in internal/translationmemory/scorer.go, so the
 *  panel never spends longer diffing a pair than the server spent scoring it. */
export const MAX_DIFF_LENGTH = 2048

/**
 * A small first-in-first-out memo. Cheap insurance against a pathological
 * pair: the panels re-render on every sidebar render, and diff-match-patch is
 * O(n·m) in the worst case. Keys join their parts with NUL, which segment
 * text cannot contain, so ("ab","c") and ("a","bc") never collide.
 */
export function boundedCache<T>(limit = 200): (key: string, compute: () => T) => T {
  const cache = new Map<string, T>()
  return (key, compute) => {
    const hit = cache.get(key)
    if (hit !== undefined) return hit
    const value = compute()
    if (cache.size >= limit) cache.delete(cache.keys().next().value!)
    cache.set(key, value)
    return value
  }
}

const dmp = new diff_match_patch()
dmp.Diff_Timeout = 0.2

/** Word diffs run on one character per word, so an edit cost of 2 folds a
 *  lone space between two changed words into the change ("fue enviado" →
 *  "se envió" reads as one replacement) while keeping a shared word apart. */
const wordDmp = new diff_match_patch()
wordDmp.Diff_Timeout = 0.2
wordDmp.Diff_EditCost = 2

const segmenter = new Intl.Segmenter(undefined, { granularity: 'word' })

const tooLong = (a: string, b: string) => a.length > MAX_DIFF_LENGTH || b.length > MAX_DIFF_LENGTH

/** Character-level diff with semantic cleanup. [] past the length cap. */
export function charDiff(reference: string, text: string): DiffOp[] {
  if (tooLong(reference, text)) return []
  const diffs = dmp.diff_main(reference, text)
  dmp.diff_cleanupSemantic(diffs)
  return diffs
}

/**
 * Word-level diff: a changed word is reported whole, never split around the
 * letters it shares with its replacement. Words come from `Intl.Segmenter`,
 * so scripts written without spaces (Japanese, Chinese, Thai) split into
 * words too. Spaces and punctuation are words of their own. [] past the
 * length cap.
 */
export function wordDiff(reference: string, text: string): DiffOp[] {
  if (tooLong(reference, text)) return []
  // Each distinct word becomes one character, the diff runs on those, and
  // the result is mapped back — diff-match-patch's own line-mode trick.
  const words: string[] = []
  const codes = new Map<string, string>()
  const encode = (s: string) => {
    let out = ''
    for (const { segment } of segmenter.segment(s)) {
      let code = codes.get(segment)
      if (code === undefined) {
        code = String.fromCharCode(words.length)
        codes.set(segment, code)
        words.push(segment)
      }
      out += code
    }
    return out
  }
  const diffs = wordDmp.diff_main(encode(reference), encode(text), false)
  wordDmp.diff_cleanupEfficiency(diffs)
  return diffs.map(([op, coded]): DiffOp => {
    let decoded = ''
    for (let i = 0; i < coded.length; i++) decoded += words[coded.charCodeAt(i)]
    return [op, decoded]
  })
}
