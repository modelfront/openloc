// "Move or copy" for one chart — the chart-card twin of the
// folder action on dashboard cards.
//
// Two submenus, each listing the dashboards the chart may go to. The lists are
// computed from the same rules the server enforces, so the menu never offers a
// target that would 403:
//
//   Move  needs a write on BOTH ends, since the chart leaves its dashboard.
//         Offered only when the reader can write the current one, and never
//         lists the current one.
//   Copy  needs a write on the DESTINATION only — anyone who can see a chart
//         may copy it into their own dashboard. The current dashboard is
//         listed too: copying beside the original is how you make a variant.
//
// Writable means what mayWrite means: your own personal dashboards, and the
// shared ones for owners and admins.
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FolderInput } from 'lucide-react'
import { copyChart, getMe, listDashboards, moveChart, type DashboardSummary } from '@/api'
import { ApiError } from '@/api/request'
import { queryKeys } from '@/lib/query-keys'
import { isAdminRole } from '@/lib/roles'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { hoverActionClass } from '@/components/list/hover-action'
import { Button } from '@/components/ui/button'
import ConfirmDialog from '@/components/ConfirmDialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type Action = 'move' | 'copy'

/** The dashboards a chart may be moved or copied to, by the server's rules. */
export function chartTargets(
  dashboards: DashboardSummary[],
  currentId: number,
  admin: boolean,
  canWriteCurrent: boolean,
): Record<Action, DashboardSummary[]> {
  const writable = dashboards.filter(d => d.personal || admin)
  return {
    move: canWriteCurrent ? writable.filter(d => d.id !== currentId) : [],
    copy: writable,
  }
}

/** The server's "your filter would be overridden" 409, which a retry can confirm. */
function overrideMessage(err: unknown): string | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body as { error?: { code?: string; message?: string } } | undefined
  return body?.error?.code === 'filter_override' ? (body.error.message ?? err.message) : null
}

export function MoveCopyChartMenu({ chartId, chartTitle, dashboardId, canWriteCurrent }: {
  chartId: number
  chartTitle: string
  dashboardId: number
  /** mayWrite on the chart's own dashboard — what moving it away requires. */
  canWriteCurrent: boolean
}) {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  // The list page's own query — usually already cached from the way here.
  const { data: dashboards } = useQuery({ queryKey: queryKeys.dashboards.list, queryFn: listDashboards })

  // The destination's own control row may take over a filter this chart fixes
  // itself, so its numbers would change. The server says which (a 409), and the
  // user decides in a confirmation rather than being refused outright.
  const [override, setOverride] = useState<{ action: Action; target: DashboardSummary; warning: string } | null>(null)

  const run = async ({ action, target, confirm }: { action: Action; target: DashboardSummary; confirm: boolean }) => {
    try {
      await (action === 'move' ? moveChart(chartId, target.id, confirm) : copyChart(chartId, target.id, confirm))
    } catch (err) {
      const warning = overrideMessage(err)
      if (!warning) throw err
      return { action, target, warning }
    }
    return { action, target, warning: null }
  }

  const mut = useMutationWithToast({
    mutationFn: run,
    successMessage: ({ action, target, warning }) => warning
      ? null
      : `${action === 'move' ? 'Moved' : 'Copied'} "${chartTitle}" to ${target.id === dashboardId ? 'this dashboard' : `"${target.name}"`}`,
    errorMessage: 'Failed to move or copy the chart',
    // Both ends change: the source loses a card on a move, the target gains one.
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: ({ action, target, warning }) => { if (warning) setOverride({ action, target, warning }) },
  })

  const confirmDialog = (
    <ConfirmDialog
      open={!!override}
      onOpenChange={open => { if (!open) setOverride(null) }}
      title={override?.action === 'move' ? 'Move this chart?' : 'Copy this chart?'}
      description={override?.warning ?? ''}
      actionLabel={override?.action === 'move' ? 'Move anyway' : 'Copy anyway'}
      variant="default"
      onConfirm={() => {
        if (override) mut.mutate({ action: override.action, target: override.target, confirm: true })
        setOverride(null)
      }}
    />
  )

  if (!dashboards) return null
  const targets = chartTargets(dashboards, dashboardId, isAdminRole(me?.role), canWriteCurrent)
  if (targets.move.length === 0 && targets.copy.length === 0) return null

  const list = (action: Action) => (
    <TargetList
      targets={targets[action]}
      currentId={dashboardId}
      testId={`chart-${action}-to`}
      onPick={target => mut.mutate({ action, target, confirm: false })}
    />
  )

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              className={hoverActionClass('muted')}
              title="Move or copy to another dashboard"
              aria-label={`Move or copy ${chartTitle}`}
              data-testid="chart-folder-menu"
              disabled={mut.isPending}
            />
          }
        >
          <FolderInput className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {targets.move.length > 0 && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger data-testid="chart-move">Move to</DropdownMenuSubTrigger>
              <DropdownMenuSubContent>{list('move')}</DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
          {targets.copy.length > 0 && (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger data-testid="chart-copy">Copy to</DropdownMenuSubTrigger>
              <DropdownMenuSubContent>{list('copy')}</DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {confirmDialog}
    </>
  )
}

/**
 * The targets under the same two headings as the list page's tabs, so a
 * dashboard is found where the reader already knows it lives.
 */
function TargetList({ targets, currentId, testId, onPick }: {
  targets: DashboardSummary[]
  currentId: number
  testId: string
  onPick: (target: DashboardSummary) => void
}) {
  const sides = [
    { label: 'Organization dashboards', items: targets.filter(d => !d.personal) },
    { label: 'My dashboards', items: targets.filter(d => d.personal) },
  ].filter(s => s.items.length > 0)

  return sides.map((side, i) => (
    <DropdownMenuGroup key={side.label}>
      {i > 0 && <DropdownMenuSeparator />}
      <DropdownMenuLabel>{side.label}</DropdownMenuLabel>
      {side.items.map(d => (
        <DropdownMenuItem key={d.id} onClick={() => onPick(d)} data-testid={`${testId}-${d.id}`}>
          <span className="truncate">{d.name}</span>
          {d.id === currentId && <span className="ml-auto shrink-0 text-xs text-gray-400">this dashboard</span>}
        </DropdownMenuItem>
      ))}
    </DropdownMenuGroup>
  ))
}
