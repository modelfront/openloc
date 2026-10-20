// The Pinned tab: the person's own dashboards of pinned charts. Their order,
// names and arrangement are the person's alone; the originals never change.
import { useState } from 'react'
import { Pencil } from 'lucide-react'
import { deletePinnedDashboard, renamePinnedDashboard, reorderPinnedDashboards, type PinnedDashboardSummary } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { Button } from '@/components/ui/button'
import { hoverActionClass } from '@/components/list/hover-action'
import { EmptyState } from '@/components/layout/EmptyState'
import { DashboardCard, DashboardCardGrid } from './DashboardCardGrid'
import { PinButton } from './PinButton'
import { PINNED_DASHBOARDS_PATH } from './dashboard-paths'

export function PinnedDashboardsList({ items }: { items: PinnedDashboardSummary[] }) {
  const [renaming, setRenaming] = useState<number | null>(null)
  const reorderMut = useMutationWithToast({
    mutationFn: (ids: number[]) => reorderPinnedDashboards(ids),
    successMessage: 'Order saved',
    errorMessage: 'Failed to save the order',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const renameMut = useMutationWithToast({
    mutationFn: ({ id, name }: { id: number; name: string }) => renamePinnedDashboard(id, name),
    successMessage: 'Dashboard renamed',
    errorMessage: 'Failed to rename the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const unpinMut = useMutationWithToast({
    mutationFn: (id: number) => deletePinnedDashboard(id),
    successMessage: 'Dashboard unpinned',
    errorMessage: 'Failed to unpin the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })

  if (items.length === 0) {
    return <EmptyState message="Nothing pinned. Pin a dashboard or a chart to keep it here." />
  }
  return (
    <DashboardCardGrid items={items} onReorder={ids => reorderMut.mutate(ids)}>
      {d => (
        <DashboardCard
          key={d.id}
          item={d}
          href={`${PINNED_DASHBOARDS_PATH}/${d.id}`}
          renaming={renaming === d.id}
          onRename={name => renameMut.mutate({ id: d.id, name })}
          onRenameDone={() => setRenaming(null)}
          pin={
            <PinButton
              pinned
              subject={d.name}
              loading={unpinMut.isPending}
              onToggle={() => {
                if (window.confirm(`Unpin "${d.name}"? Its charts leave Pinned; the originals stay.`)) unpinMut.mutate(d.id)
              }}
            />
          }
          actions={
            <Button
              variant="ghost"
              size="icon-sm"
              className={hoverActionClass('muted')}
              title="Rename dashboard"
              aria-label={`Rename ${d.name}`}
              data-testid="dashboard-rename"
              onClick={() => setRenaming(d.id)}
            >
              <Pencil className="h-4 w-4" />
            </Button>
          }
        />
      )}
    </DashboardCardGrid>
  )
}
