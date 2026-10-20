/**
 * Which segments a word-level spell fix should touch.
 *
 * Backs the scope rows in the inline fix menu ("this segment" / "non-locked
 * segments" / "all segments"). Pure so the counts shown on the buttons and the
 * rows actually written can't drift apart — the menu label and the mutation come
 * from the same call.
 */

/**
 * One scope vocabulary for both spell check surfaces, so the locked rule is
 * written once. The narrowed aliases below keep each surface honest about what
 * it offers while the meaning of each value lives here.
 */
export type SpellcheckScope = 'segment' | 'all' | 'nonlocked' | 'filtered' | 'selected'

/** What the inline fix panel offers for a single word. */
export type SpellFixScope = Extract<SpellcheckScope, 'segment' | 'all' | 'nonlocked'>

/** What the review dialog offers for a whole-job run. */
export type SpellRunScope = Extract<SpellcheckScope, 'all' | 'nonlocked' | 'filtered' | 'selected'>

/** The review dialog's scope unless the user picks another. */
export const DEFAULT_RUN_SCOPE: SpellRunScope = 'nonlocked'

/**
 * THE definition of which scope touches locked segments: only "all", which the
 * UI labels "includes locked". Every other scope skips them. Both resolvers and
 * the `include_locked` request flag read it, so "which segments get checked"
 * and "which segments get written" can't drift apart.
 */
export function scopeIncludesLocked(scope: SpellcheckScope): boolean {
  return scope === 'all'
}

/**
 * The scope a run may open on. Writing through a lock is for owners and
 * admins, so for anyone else the all scope falls back to the default.
 */
export function allowedRunScope(scope: SpellRunScope, canWriteLocked: boolean): SpellRunScope {
  return scopeIncludesLocked(scope) && !canWriteLocked ? DEFAULT_RUN_SCOPE : scope
}

export interface ScopeCandidate {
  id: number
  active_text: string | null
  locked?: boolean
}

/**
 * Whole-word occurrence test.
 *
 * A substring match would rewrite `adresse` inside `Postadresse`, which is a
 * different (correct) word. Word boundaries are computed with a Unicode-aware
 * check rather than `\b`, because `\b` treats `ä`/`ü` as boundaries and would
 * match the tail of `Lieferadresse`.
 */
export function containsWord(text: string, word: string): boolean {
  return wordOccurrences(text, word) > 0
}

/**
 * Plain-text offsets of every whole-word occurrence.
 *
 * Lets a word-level fix reuse the offset-based, tag-safe write path
 * (`buildSpellFixes`) instead of doing its own string surgery on rich text.
 */
export function wordOffsets(text: string, word: string): number[] {
  const out: number[] = []
  if (!text || !word) return out
  let from = 0
  for (;;) {
    const idx = text.indexOf(word, from)
    if (idx < 0) return out
    const before = idx === 0 ? '' : text[idx - 1]
    const after = text[idx + word.length] ?? ''
    if (!isWordChar(before) && !isWordChar(after)) out.push(idx)
    from = idx + word.length
  }
}

/** Actions the inline fix menu delegates upward to the page. */
export interface SpellFixActions {
  /**
   * Whether the user may write locked segments: owners and admins, the roles
   * that may unlock. The server reads `include_locked` as false for anyone
   * else, so both spell check surfaces offer the all scope to them disabled,
   * with the reason.
   */
  canWriteLocked: boolean
  /** Occurrence counts across the file, for the menu's button labels. */
  counts: (word: string) => ScopeCounts
  /**
   * Write the fix to other segments in the file. The open segment is handled by
   * the editor itself, so it's excluded here. Resolves with the row count written.
   */
  applyScope: (word: string, replacement: string, scope: SpellFixScope) => Promise<number>
  /**
   * Persist the word to the project dictionary so future checks skip it.
   * Resolves true on success; the caller reports failures itself.
   */
  ignoreInProject: (word: string) => Promise<boolean>
}

export function wordOccurrences(text: string, word: string): number {
  if (!text || !word) return 0
  let count = 0
  let from = 0
  for (;;) {
    const idx = text.indexOf(word, from)
    if (idx < 0) return count
    const before = idx === 0 ? '' : text[idx - 1]
    const after = text[idx + word.length] ?? ''
    if (!isWordChar(before) && !isWordChar(after)) count++
    from = idx + word.length
  }
}

function isWordChar(ch: string): boolean {
  if (!ch) return false
  // Letters, marks and digits count as word characters; punctuation and
  // whitespace don't. Covers umlauts, accents and non-Latin scripts.
  return /[\p{L}\p{M}\p{N}]/u.test(ch)
}

/**
 * Translations a fix should be written to, for a given scope. Locked rows are
 * targets only for the "all" scope.
 *
 * `excludeId` drops the segment currently open in an editor: that one is fixed
 * through the editor itself so its unsaved draft stays coherent, rather than
 * being written underneath it by a bulk request.
 */
export function selectScopeTargets(
  candidates: ScopeCandidate[],
  word: string,
  scope: SpellFixScope,
  excludeId?: number,
): ScopeCandidate[] {
  if (scope === 'segment') return []
  return candidates.filter(c => {
    if (c.id === excludeId) return false
    if (c.locked && !scopeIncludesLocked(scope)) return false
    return containsWord(c.active_text ?? '', word)
  })
}

export interface ScopeCounts {
  /** Every segment the word appears in, locked and the open one included. */
  all: number
  /** Same, minus locked segments. */
  nonlocked: number
  /** How many of those are locked. */
  locked: number
}

/**
 * Counts for the panel labels. Each includes the open segment, so the number on
 * a button equals the rows its write covers.
 */
export function countScope(candidates: ScopeCandidate[], word: string): ScopeCounts {
  let all = 0
  let nonlocked = 0
  let locked = 0
  for (const c of candidates) {
    if (!containsWord(c.active_text ?? '', word)) continue
    all++
    if (c.locked) locked++
    else nonlocked++
  }
  return { all, nonlocked, locked }
}


/** A translation as the run resolver needs it. */
interface RunCandidate {
  id: number
  locked?: boolean
}

/**
 * Resolve a run scope to an ordered list of translation ids — the dialog's
 * counterpart to `selectScopeTargets`.
 *
 * The two resolvers answer different questions (this one "which segments do we
 * check", the other "which segments contain this word"), which is why they stay
 * separate functions — but they share the locked rule via `scopeIncludesLocked`.
 *
 * `filteredIndices` comes straight from CATEditor, so the `filtered` scope needs
 * no predicate of its own — the filter bar already computed it. `selected` is
 * the editor's segment selection (translation ids) among the visible rows.
 */
export function resolveRunScope(
  scope: SpellRunScope,
  filteredIndices: number[],
  segmentIds: number[],
  translationBySegment: Map<number, RunCandidate>,
  selected: ReadonlySet<number> = new Set(),
): number[] {
  const indices = scope === 'filtered' || scope === 'selected' ? filteredIndices : segmentIds.map((_, i) => i)

  const ids: number[] = []
  for (const idx of indices) {
    const segmentId = segmentIds[idx]
    if (segmentId === undefined) continue
    const translation = translationBySegment.get(segmentId)
    if (!translation) continue
    if (translation.locked && !scopeIncludesLocked(scope)) continue
    if (scope === 'selected' && !selected.has(translation.id)) continue
    ids.push(translation.id)
  }
  return ids
}
