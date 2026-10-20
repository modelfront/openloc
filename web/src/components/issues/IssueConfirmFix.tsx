import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { confirmIssueFixed, type IssueDetail } from '@/api'
import { Button } from '@/components/ui/button'

/**
 * For the reporter's org, on a resolved issue nobody confirmed yet: is it
 * fixed? Confirmed fixed records it; Still broken points at the reply box,
 * and the reply reopens the issue.
 */
export function IssueConfirmFix({ issueId, onConfirmed, onStillBroken }: {
  issueId: number
  onConfirmed: (d: IssueDetail) => void
  onStillBroken: () => void
}) {
  const mut = useMutationWithToast({
    mutationFn: () => confirmIssueFixed(issueId),
    successMessage: 'Thanks for confirming the fix.',
    onSuccess: onConfirmed,
  })
  return (
    <section className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-card px-3 py-2"
      data-testid="issue-confirm-fix">
      <p className="text-sm">The support team resolved this issue. Is it fixed for you?</p>
      <div role="group" aria-label="Is it fixed?" className="flex gap-2">
        <Button onClick={() => mut.mutate()} loading={mut.isPending} data-testid="issue-confirm-fixed">Confirmed fixed</Button>
        <Button variant="outline" onClick={onStillBroken} disabled={mut.isPending} data-testid="issue-still-broken">
          Still broken
        </Button>
      </div>
    </section>
  )
}
