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
  /** The file, and its job, of a translation: the write goes to each file's endpoint. */
  fileOf: (translationId: number) => { jobId: number; fileId: number } | undefined
  errors: SpellError[]
  replacement: string
  /** Write locked segments too (owner/admin scope). */
  includeLocked: boolean
  lookup: (translationId: number) => TranslationTextRow | undefined
  /** A job's task refused the write. */
  onTaskLocked: (err: ApiError, jobId: number) => void
  /** The write reopened a job's completed task. */
  onTaskReopened: (jobId: number) => void
}

/**
 * Writes the corrections as drafts, one request per file, and warns about the
 * ones left out: a correction across an inline tag or on text changed since
 * the check, a segment changed meanwhile, locked, or open in someone else's
 * editor. Resolves to the number of segments corrected, or null when every
 * file's task refused the write (`onTaskLocked` has reported each).
 */
export async function writeSpellFixes({
  fileOf, errors, replacement, includeLocked, lookup, onTaskLocked, onTaskReopened,
}: WriteSpellFixesArgs): Promise<number | null> {
  const { fixes, unfixable } = buildSpellFixes(errors, replacement, lookup)
  if (unfixable.length > 0) {
    toast.warning(
      `${unfixable.length} correction${unfixable.length === 1 ? '' : 's'} skipped — ` +
      'the text spans an inline tag or changed since the check',
    )
  }
  if (fixes.length === 0) return 0

  const byFile = new Map<number, { jobId: number; fixes: typeof fixes }>()
  for (const fix of fixes) {
    const file = fileOf(fix.translation_id)
    if (!file) continue
    const entry = byFile.get(file.fileId)
    if (entry) entry.fixes.push(fix)
    else byFile.set(file.fileId, { jobId: file.jobId, fixes: [fix] })
  }

  // One file's refusal or failure never stops the others.
  let written = false
  let refused = false
  const failed: unknown[] = []
  let applied = 0, conflicts = 0, locked = 0, held = 0
  for (const [fileId, { jobId, fixes: part }] of byFile) {
    const result = await batchDraftTranslations(fileId, part, { includeLocked })
      .catch((err: unknown) => {
        if (isTaskLockedError(err)) {
          refused = true
          onTaskLocked(err, jobId)
        } else {
          failed.push(err)
        }
        return null
      })
    if (!result) continue
    written = true
    if (result.taskReopened) onTaskReopened(jobId)
    applied += result.applied.length
    conflicts += result.conflicts.length
    locked += result.locked.length
    held += result.held.length
  }
  // Nothing written and no task refused it: the error says why.
  if (!written && !refused && failed.length > 0) throw failed[0]
  if (failed.length > 0) toast.warning(`${count(failed.length, 'file')} could not be saved, try again`)
  if (!written) return null
  if (conflicts > 0) {
    toast.warning(
      `${conflicts} segment${conflicts === 1 ? '' : 's'} ` +
      'changed since the check — run it again to pick them up',
    )
  }
  // A scope that skips locked rows never checks them, so this only fires for
  // a row locked after the check ran.
  if (locked > 0) {
    toast.warning(`${count(locked, 'locked segment')} skipped`)
  }
  warnHeldSkipped(held)
  return applied
}
