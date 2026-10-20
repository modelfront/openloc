import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { updateTermBase } from '../../api'
import { queryKeys } from '../../lib/query-keys'

// Base-level mutations for the terminology browser. Per-concept/term edits live
// in use-concept-mutations.ts.
export function useTerminologyMutations(terminologyId: number) {
  const renameMut = useMutationWithToast({
    mutationFn: ({ name, description }: { name: string; description: string }) =>
      updateTermBase(terminologyId, name, description),
    successMessage: 'Term base updated',
    errorMessage: 'Update failed',
    invalidateKeys: [queryKeys.terminology.all],
  })

  return { renameMut }
}
