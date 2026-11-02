import { useCallback, useMemo } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { addSpellcheckDictionaryWord, EDITOR_MAX_ROWS, getEditorRows, getWordCandidates, type EditorRow, type Job } from '../api'
import { errorText } from '@/lib/error-text'
import { queryKeys } from '../lib/query-keys'
import { invalidateJobContext } from '../lib/query-invalidation'
import { chunk, type SpellError } from '../lib/spellcheck-run'
import {
  countScope, scopeIncludesLocked, selectScopeTargets, wordOffsets,
  type SpellFixActions, type SpellFixScope,
} from '../lib/spellcheck-scope'
import { writeSpellFixes } from '../lib/spellcheck-write'
import { handleTaskLocked, handleTaskReopened } from '../lib/task-edit'
import { useMutationWithToast } from './use-mutation-with-toast'

/**
 * The CAT editor's spell corrections: the review dialog's writes and the word
 * actions of the inline fix menu. `activeTranslationId` is the open segment,
 * which the menu fixes through its own editor.
 */
export function useSpellFixes({ jid, pid, job, isAdmin, activeTranslationId }: {
  jid: number
  pid: number
  job: Pick<Job, 'file_id' | 'project_id' | 'target_language'> | undefined
  isAdmin: boolean
  activeTranslationId: number | undefined
}) {
  const queryClient = useQueryClient()
  const fileId = job?.file_id ?? 0
  const projectId = job?.project_id
  const targetLanguage = job?.target_language

  /**
   * Apply spell corrections through the bulk draft endpoint.
   *
   * Each correction is written against the row as the server has it now — its
   * text and `updated_at` — read just before the write, so a segment changed
   * meanwhile is reported as a conflict rather than overwritten.
   *
   * Routed through `useMutationWithToast` per the project convention; the success
   * message is a function of the result because the count isn't known until the
   * write returns. Conflict / unfixable warnings are raised inside the mutation
   * because they're partial-success notes, not failures. Resolves null when
   * the task refused the write, which handleTaskLocked has already reported.
   */
  const spellFixMut = useMutationWithToast<number | null, {
    errors: SpellError[]
    replacement: string
    includeLocked: boolean
  }>({
    mutationFn: async ({ errors, replacement, includeLocked }) => {
      const segmentIds = [...new Set(errors.map(e => e.segmentId))]
      const current = new Map<number, EditorRow['translation']>()
      for (const ids of chunk(segmentIds, EDITOR_MAX_ROWS)) {
        for (const row of await getEditorRows(jid, ids)) {
          if (row.translation) current.set(row.translation.id, row.translation)
        }
      }
      return writeSpellFixes({
        fileId,
        errors,
        replacement,
        includeLocked,
        lookup: id => current.get(id) ?? undefined,
        onTaskLocked: err => handleTaskLocked(queryClient, pid, jid, err),
        onTaskReopened: () => handleTaskReopened(queryClient, pid, jid),
      })
    },
    successMessage: applied =>
      applied === null ? null : `Corrected ${applied} segment${applied === 1 ? '' : 's'}`,
    errorMessage: 'Could not apply the correction',
    // One call covers the job's translations, detail and analysis caches.
    onSuccess: () => invalidateJobContext(queryClient, pid, jid),
  })

  const applySpellFixes = useCallback(async (
    errors: SpellError[], replacement: string, includeLocked: boolean,
  ): Promise<number> => {
    if (errors.length === 0) return 0
    return (await spellFixMut.mutateAsync({ errors, replacement, includeLocked })) ?? 0
  }, [spellFixMut])

  // --- Word-level fixes from the inline right-click menu ---
  //
  // The open segment is fixed through the editor itself (see RichTextCellEditor),
  // so these only cover the OTHER segments — writing the open row underneath a
  // mounted editor would race its autosave.
  //
  // The translations whose target holds a word somewhere, from the server; the
  // whole-word checks below narrow them.
  const wordCandidates = useCallback((word: string) => queryClient.fetchQuery({
    queryKey: queryKeys.job.editorWord(jid, word),
    queryFn: () => getWordCandidates(jid, word),
  }), [queryClient, jid])

  const spellFixActions = useMemo<SpellFixActions>(() => ({
    // The server writes through a lock for owners and admins only — the same
    // tier that may unlock — so both spell check surfaces offer the all scope
    // to them alone.
    canWriteLocked: isAdmin,
    counts: async word => countScope(await wordCandidates(word), word),

    applyScope: async (word, replacement, scope: SpellFixScope) => {
      const targets = selectScopeTargets(await wordCandidates(word), word, scope, activeTranslationId)
      if (targets.length === 0) return 0

      // Synthesize one finding per whole-word occurrence and reuse the same
      // tag-safe builder the review flow uses, rather than hand-rolling string
      // surgery over rich text here.
      const errors: SpellError[] = []
      for (const t of targets) {
        const text = t.active_text ?? ''
        for (const offset of wordOffsets(text, word)) {
          errors.push({
            translationId: t.id,
            segmentId: t.segment_id,
            segmentIndex: -1,
            offset,
            length: word.length,
            word,
            message: '',
            suggestions: [replacement],
            ruleCategory: '',
            targetText: text,
            sourceText: '',
          })
        }
      }

      return applySpellFixes(errors, replacement, scopeIncludesLocked(scope))
    },

    ignoreInProject: async word => {
      if (!projectId || !targetLanguage) return false
      try {
        await addSpellcheckDictionaryWord(projectId, targetLanguage, word)
        toast.success(`"${word}" added to the project dictionary`)
        return true
      } catch (err) {
        toast.error(errorText(err, `Could not save "${word}"`))
        return false
      }
    },
  }), [isAdmin, wordCandidates, activeTranslationId, applySpellFixes, projectId, targetLanguage])

  return { applySpellFixes, spellFixActions }
}
