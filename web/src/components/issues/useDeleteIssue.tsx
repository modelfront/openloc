import { useState } from 'react'
import { deleteIssue, type Issue, type IssueScope } from '@/api'
import { issuesKey, queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import ConfirmDialog from '@/components/ConfirmDialog'

/**
 * Deleting an issue, from the list or its own page: `ask` opens the
 * confirmation, and `dialog` is rendered once by the caller.
 */
export function useDeleteIssue(scope: IssueScope, onDeleted?: () => void) {
  const [target, setTarget] = useState<Pick<Issue, 'id' | 'title'> | null>(null)
  const mut = useMutationWithToast({
    mutationFn: (id: number) => deleteIssue(scope, id),
    successMessage: 'Issue deleted',
    errorMessage: 'Failed to delete the issue',
    // The lists and the issue's own page live under the scope's key; the
    // support team's tag filter lists only the tags of issues still there.
    invalidateKeys: scope === 'platform' ? [issuesKey(scope), queryKeys.platform.issueTags] : [issuesKey(scope)],
    onSuccess: () => onDeleted?.(),
  })
  const dialog = (
    <ConfirmDialog
      open={target !== null}
      onOpenChange={open => { if (!open) setTarget(null) }}
      title="Delete this issue?"
      description={target ? `"${target.title}" and its conversation will be removed from the list.` : ''}
      actionLabel="Delete"
      onConfirm={() => {
        if (target) mut.mutate(target.id)
        setTarget(null)
      }}
    />
  )
  return { ask: setTarget, dialog, deleting: mut.isPending }
}
