// Writing the spell check's corrections: one draft per segment, then a
// warning for each kind of segment the write left out.

import { toast } from 'sonner'
import { batchDraftTranslations } from '../api/spellcheck'
import { buildSpellFixes, type TranslationTextRow } from './spellcheck-apply'
import type { SpellError } from './spellcheck-run'
import { warnHeldSkipped } from './segment-bulk'
import type { ApiError } from '../api/request'
import { isTaskLockedError } from './task-edit'
import { count } from './plural'

export interface WriteSpellFixesArgs {
  fileId: number
  errors: SpellError[]
  replacement: string
  /** Write locked segments too (owner/admin scope). */
  includeLocked: boolean
  lookup: (translationId: number) => TranslationTextRow | undefined
  /** The task refused the write. */
  onTaskLocked: (err: ApiError) => void
  /** The write reopened a completed task. */
  onTaskReopened: () => void
}

/**
 * Writes the corrections as drafts and warns about the ones left out: a
 * correction across an inline tag or on text changed since the check, a
 * segment changed meanwhile, locked, or open in someone else's editor.
 * Resolves to the number of segments corrected, or null when the task refused
 * the write (`onTaskLocked` has reported it).
 */
export async function writeSpellFixes({
  fileId, errors, replacement, includeLocked, lookup, onTaskLocked, onTaskReopened,
}: WriteSpellFixesArgs): Promise<number | null> {
  const { fixes, unfixable } = buildSpellFixes(errors, replacement, lookup)
  if (unfixable.length > 0) {
    toast.warning(
      `${unfixable.length} correction${unfixable.length === 1 ? '' : 's'} skipped — ` +
      'the text spans an inline tag or changed since the check',
    )
  }
  if (fixes.length === 0) return 0

  const result = await batchDraftTranslations(fileId, fixes, { includeLocked })
    .catch((err: unknown) => {
      if (!isTaskLockedError(err)) throw err
      onTaskLocked(err)
      return null
    })
  if (!result) return null
  if (result.taskReopened) onTaskReopened()
  if (result.conflicts.length > 0) {
    toast.warning(
      `${result.conflicts.length} segment${result.conflicts.length === 1 ? '' : 's'} ` +
      'changed since the check — run it again to pick them up',
    )
  }
  // A scope that skips locked rows never checks them, so this only fires for
  // a row locked after the check ran.
  if (result.locked.length > 0) {
    toast.warning(`${count(result.locked.length, 'locked segment')} skipped`)
  }
  warnHeldSkipped(result.held.length)
  return result.applied.length
}
