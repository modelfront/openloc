import { useMutationWithToast } from './use-mutation-with-toast'
import { ignoreQAFinding, unignoreQAFinding } from '../api'
import type { QaFinding } from '../lib/qa-findings'

/**
 * The ignore / un-ignore pair for one QA finding, shared by
 * the segment-gutter dot and the sidebar's QA findings section.
 *
 * `toggle` picks the direction from the finding's current state, so a caller
 * renders one button and labels it from `issue.ignored`. Both endpoints answer
 * with the segment's full finding list, which `onDone` hands to the caller to
 * patch into the cached job report.
 */
export function useQAFindingIgnore(issue: QaFinding, onDone?: (issues: QaFinding[]) => void) {
  const translationId = issue.translation_id
  const ignoreMut = useMutationWithToast({
    mutationFn: () => ignoreQAFinding(translationId, issue.check_type),
    successMessage: 'QA finding ignored',
    errorMessage: 'Could not ignore the finding',
    onSuccess: onDone,
  })
  const unignoreMut = useMutationWithToast({
    mutationFn: () => unignoreQAFinding(translationId, issue.check_type),
    successMessage: 'QA finding restored',
    errorMessage: 'Could not restore the finding',
    onSuccess: onDone,
  })
  return {
    busy: ignoreMut.isPending || unignoreMut.isPending,
    toggle: () => (issue.ignored ? unignoreMut : ignoreMut).mutate(),
  }
}
