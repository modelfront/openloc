// "Reset to defaults" for the Organization dashboards tab.
//
// This DELETES every organization dashboard, including ones the team built, and
// installs the built-in set fresh. That is the product decision, so the job of
// this component is to make sure nobody presses it expecting a merge: the
// dialog names how many dashboards go, and says that share links and other
// people's pins die with the charts they point at.
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RotateCcw } from 'lucide-react'
import { getSharedDashboardCount, resetSharedDashboards } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import { Button } from '@/components/ui/button'
import { hoverActionClass } from '@/components/list/hover-action'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

export function ResetDefaultsButton() {
  const [open, setOpen] = useState(false)
  const { data: counts } = useQuery({
    queryKey: queryKeys.dashboards.sharedCount,
    queryFn: getSharedDashboardCount,
    enabled: open, // only when the dialog needs the numbers
  })
  const resetMut = useMutationWithToast({
    mutationFn: resetSharedDashboards,
    successMessage: 'Organization dashboards reset to defaults',
    errorMessage: 'Failed to reset the dashboards',
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: () => setOpen(false),
  })

  return (
    <>
      {/* Icon only: a rare, destructive action should be reachable without
          competing for attention with the one people press daily. The tooltip
          carries the label rather than the button. */}
      <TooltipProvider delay={200}>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="ghost"
                size="icon"
                className={hoverActionClass('muted')}
                aria-label="Reset organization dashboards to defaults"
                data-testid="reset-defaults"
                onClick={() => setOpen(true)}
              />
            }
          >
            <RotateCcw className="h-4 w-4" />
          </TooltipTrigger>
          <TooltipContent>Reset organization dashboards to defaults</TooltipContent>
        </Tooltip>
      </TooltipProvider>

      <Dialog open={open} onOpenChange={setOpen} keepOpen={resetMut.isPending}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reset organization dashboards?</DialogTitle>
            <DialogDescription>
              {counts
                ? `This deletes all ${counts.shared} organization dashboard${counts.shared === 1 ? '' : 's'} — including any your team created — and replaces them with the ${counts.defaults} built-in ones.`
                : 'This deletes every organization dashboard — including any your team created — and replaces them with the built-in ones.'}
            </DialogDescription>
          </DialogHeader>
          {/* The consequence people do not think of: a share link points at a
              CHART, and the charts are deleted with their dashboards. */}
          <p className="text-sm text-gray-600 dark:text-gray-300">
            Any share links to charts on those dashboards will stop working.
            {counts && counts.pinned_users > 0 && ` The charts also leave ${counts.pinned_users} ${counts.pinned_users === 1 ? "person's" : "people's"} Pinned.`}
            {' '}Your personal dashboards are not affected.
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={resetMut.isPending}>Cancel</Button>
            <Button
              variant="destructive"
              data-testid="reset-defaults-confirm"
              loading={resetMut.isPending}
              onClick={() => resetMut.mutate()}
            >
              Delete and reset
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
