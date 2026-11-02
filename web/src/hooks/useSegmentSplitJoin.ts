import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { joinSegments, splitSegment, type JoinRow, type SegmentEditResult, type SplitRequest } from '../api'
import type { IndexLookup } from '../lib/editor-index'
import { invalidateJobContext } from '../lib/query-invalidation'
import { queryKeys } from '../lib/query-keys'
import { loadedVersion } from '../lib/segment-edit'
import { taskLockFailure } from '../lib/task-edit'
import { useMutationWithToast } from './use-mutation-with-toast'
import { refetchIndexWithPages } from './useEditorRows'

interface Options {
  jobId: number
  projectId: number | null
  /** The editor index's lookup: the edited rows are found in it once the
   *  index is fetched again. */
  lookup: IndexLookup
  /** Opens and shows the row of a translation. */
  openTranslation: (translationId: number) => void
}

/**
 * Split and join for the CAT editor. Both replace rows: the new index comes
 * in with the pages on screen already loaded, so no row shows as loading, then
 * the rest of the job context is fetched again, and the first row that now
 * stands where the edited ones were opens.
 */
export function useSegmentSplitJoin({ jobId, projectId, lookup, openTranslation }: Options) {
  const queryClient = useQueryClient()
  const reopenRef = useRef<number | null>(null)

  useEffect(() => {
    const id = reopenRef.current
    if (id === null || !lookup.byTranslation.has(id)) return
    reopenRef.current = null
    openTranslation(id)
  }, [lookup, openTranslation])

  const lockFailure = taskLockFailure(queryClient, projectId ?? 0, jobId)
  const invalidate = (editorRowsFresh: boolean) => {
    if (projectId) invalidateJobContext(queryClient, projectId, jobId, { editorRowsFresh })
    else if (!editorRowsFresh) void queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jobId) })
  }
  const done = (res: SegmentEditResult) => {
    reopenRef.current = res.translation_ids[0] ?? null
    // The rest of the job context follows the early fetch, which leaves the
    // editor's index and pages fresh. When it fails, the invalidation brings
    // the new index; its rows then show as loading until their pages arrive.
    void refetchIndexWithPages(queryClient, jobId).then(() => invalidate(true), () => invalidate(false))
  }

  const split = useMutationWithToast<SegmentEditResult, SplitRequest>({
    mutationFn: req => splitSegment(jobId, req),
    successMessage: 'Segment split.',
    errorMessage: 'Split failed',
    errorToast: lockFailure.errorToast,
    onError: lockFailure.onError,
    onSuccess: done,
  })
  const join = useMutationWithToast<SegmentEditResult, JoinRow[]>({
    mutationFn: rows => joinSegments(jobId, rows),
    successMessage: 'Segments joined.',
    errorMessage: 'Join failed',
    errorToast: lockFailure.errorToast,
    onError: lockFailure.onError,
    onSuccess: done,
  })

  /** Joins the rows of these translations, in document order, with the
   *  versions the editor holds now; `versions` gives a row's version the
   *  caller already knows (the open row's, after its draft save). */
  const joinTranslations = (ids: readonly number[], versions: Readonly<Record<number, string>> = {}) => {
    const rows: JoinRow[] = []
    for (const id of ids) {
      const version = versions[id] ?? loadedVersion(queryClient, jobId, id)
      if (!version) {
        toast.error('Scroll the rows into view, then join them again.')
        return
      }
      rows.push({ translation_id: id, expected_updated_at: version })
    }
    join.mutate(rows)
  }

  return { split, joinTranslations, pending: split.isPending || join.isPending }
}
