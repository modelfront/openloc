// Dashboards list. Three tabs: Organization dashboards, which the whole org
// sees; My dashboards, visible only to their owner; and Pinned, the person's
// own references to charts that live elsewhere.
// "New dashboard" creates an empty one; charts go in from the dashboard page,
// either through the builder or by asking the chat.
import { useState } from 'react'
import { Navigate, useLocation, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Pencil, FolderInput } from 'lucide-react'
import { listDashboards, listPinnedDashboards, deleteDashboard, setDashboardVisibility, renameDashboard, reorderDashboards, duplicateDashboard, getMe, type DashboardSummary } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { usePins } from '../hooks/usePins'
import { isAdminRole } from '../lib/roles'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { NewDashboardButton } from '@/components/charts/NewChartButton'
import { ResetDefaultsButton } from '@/components/charts/ResetDefaultsButton'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import ConfirmDialog from '@/components/ConfirmDialog'
import { DeleteButton } from '@/components/ui/delete-button'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { hoverActionClass } from '@/components/list/hover-action'
import { SubNav } from '@/components/layout/SubNav'
import { EmptyState } from '@/components/layout/EmptyState'
import { DashboardCard, DashboardCardGrid } from '@/components/charts/DashboardCardGrid'
import { PinnedDashboardsList } from '@/components/charts/PinnedDashboardsList'
import { PinButton } from '@/components/charts/PinButton'
import { MY_DASHBOARDS_PATH, ORG_DASHBOARDS_PATH, PINNED_DASHBOARDS_PATH } from '@/components/charts/dashboard-paths'

export { resolveDrop } from '@/components/charts/DashboardCardGrid'

const ORG = 'Organization dashboards'
const MINE = 'My dashboards'

type Tab = 'pinned' | 'organization' | 'mine'

function tabOf(pathname: string): Tab {
  if (pathname.startsWith(PINNED_DASHBOARDS_PATH)) return 'pinned'
  if (pathname.startsWith(MY_DASHBOARDS_PATH)) return 'mine'
  return 'organization'
}

/** /dashboards: Pinned when the person has pinned something, else the org's. */
export function DashboardsLanding() {
  const [params] = useSearchParams()
  const { data: pinned, isLoading } = useQuery({ queryKey: queryKeys.dashboards.pinned, queryFn: listPinnedDashboards })
  // `?tab=mine` is an old link to My dashboards; it still opens there.
  if (params.get('tab') === 'mine') return <Navigate to={MY_DASHBOARDS_PATH} replace />
  if (isLoading) return <LoadingPlaceholder />
  return <Navigate to={pinned?.length ? PINNED_DASHBOARDS_PATH : ORG_DASHBOARDS_PATH} replace />
}

export function DashboardsPage() {
  // Each tab is its own route, so a link to "my dashboards" opens there.
  const tab = tabOf(useLocation().pathname)
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const { data: dashboards, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.dashboards.list,
    queryFn: listDashboards,
  })
  const { data: pinned, isLoading: pinnedLoading } = useQuery({
    queryKey: queryKeys.dashboards.pinned,
    queryFn: listPinnedDashboards,
  })
  const pins = usePins()
  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => deleteDashboard(id),
    successMessage: 'Dashboard deleted',
    errorMessage: 'Failed to delete dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  // One list serves both tabs: the endpoint already returns only what this user
  // may see, so switching tabs is a filter rather than a refetch.
  const visibilityMut = useMutationWithToast({
    mutationFn: ({ id, personal }: { id: number; personal: boolean }) => setDashboardVisibility(id, personal),
    successMessage: 'Dashboard moved',
    errorMessage: 'Failed to move dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  // Which card is being renamed. One at a time: two open editors on a grid is
  // a way to lose an edit, not a feature.
  const [renaming, setRenaming] = useState<number | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<DashboardSummary | null>(null)
  const reorderMut = useMutationWithToast({
    mutationFn: (ids: number[]) => reorderDashboards(ids),
    successMessage: 'Order saved',
    errorMessage: 'Failed to save the order',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const duplicateMut = useMutationWithToast({
    mutationFn: ({ id, personal }: { id: number; personal: boolean }) => duplicateDashboard(id, personal),
    successMessage: d => `Copied as "${d.name}"`,
    errorMessage: 'Failed to copy the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const renameMut = useMutationWithToast({
    mutationFn: ({ id, name }: { id: number; name: string }) => renameDashboard(id, name),
    successMessage: 'Dashboard renamed',
    errorMessage: 'Failed to rename the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })

  if (isLoading || pinnedLoading) return <LoadingPlaceholder />
  // No data at all means the list failed to load.
  const loadFailed = isError && !dashboards

  const isAdmin = isAdminRole(me?.role)
  // Move: mayWrite on the dashboard itself. Copy: the right to create on the
  // side it is going TO. Two different rules, both mirroring the server's.
  const canMove = (d: DashboardSummary) => d.personal || isAdmin
  const canCopyTo = (personal: boolean) => personal || isAdmin

  const inTab = (dashboards ?? []).filter(d => (tab === 'mine' ? d.personal : !d.personal))
  const pinnedEmpty = (pinned ?? []).length === 0
  const tabs = [
    { to: ORG_DASHBOARDS_PATH, label: ORG },
    { to: MY_DASHBOARDS_PATH, label: MINE },
    // Last, but where /dashboards opens once something is pinned. Shown even
    // when empty, so people learn where pins go; still reachable while open,
    // so unpinning the last item does not strand the page.
    {
      to: PINNED_DASHBOARDS_PATH,
      label: 'Pinned',
      disabled: pinnedEmpty && tab !== 'pinned',
      disabledReason: 'Pin a dashboard or chart to see it here',
    },
  ]

  return (
    <PageContainer>
      <PageHeader
        title="Dashboards"
        // Nothing is created in Pinned: it only holds what is pinned elsewhere.
        actions={tab !== 'pinned' && <NewDashboardButton personal={tab === 'mine'} canShare={isAdmin} />}
      />

      {/* Reset sits at the end of the TAB row rather than beside "New
          dashboard": it is a rare, destructive action, and giving it equal
          weight to the one people press every day overstates it. */}
      <SubNav
        className="mb-4"
        items={tabs}
        actions={tab === 'organization' && isAdmin && <ResetDefaultsButton />}
      />

      {tab === 'pinned' ? (
        <PinnedDashboardsList items={pinned ?? []} />
      ) : (
        <>
          {loadFailed && <LoadFailed what="dashboards" error={error} onRetry={() => refetch()} />}
          {!loadFailed && inTab.length === 0 && tab === 'mine' && (
            <EmptyState message="Nothing here yet. A dashboard you create starts out as yours; share it when it's ready." />
          )}
          <DashboardCardGrid key={tab} items={inTab} onReorder={ids => reorderMut.mutate(ids)}>
            {(d, i) => {
              const pin = pins.dashboard(d.id)
              return (
                <DashboardCard
                  key={d.id}
                  item={d}
                  href={`/dashboards/${d.id}`}
                  // Tours point at the first card, not the whole grid (see ProjectList).
                  guide={i === 0 ? 'dashboards.grid' : undefined}
                  renaming={renaming === d.id}
                  onRename={name => renameMut.mutate({ id: d.id, name })}
                  onRenameDone={() => setRenaming(null)}
                  pin={<PinButton {...pin} subject={d.name} />}
                  actions={<>
                    {/* Move and copy are the same question — which side? — so
                        they share one control rather than adding a fourth icon.
                        MOVE follows mayWrite (your own is yours to publish,
                        taking an org one private is owner/admin). COPY is gated
                        by its DESTINATION instead: copying the org's dashboard
                        into your own space is how you experiment on it without
                        touching it, so anyone who can see it may do that. */}
                    {(canMove(d) || canCopyTo(!d.personal)) && (
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              className={hoverActionClass('muted')}
                              title={d.personal ? `Move or copy to ${ORG}` : `Move or copy to ${MINE}`}
                              aria-label={`Move or copy ${d.name}`}
                              data-testid="dashboard-folder-menu"
                            />
                          }
                        >
                          <FolderInput className="h-4 w-4" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {canMove(d) && (
                            <DropdownMenuItem
                              data-testid="dashboard-move"
                              onClick={() => visibilityMut.mutate({ id: d.id, personal: !d.personal })}
                            >
                              {d.personal ? `Move to ${ORG}` : `Move to ${MINE}`}
                            </DropdownMenuItem>
                          )}
                          {canCopyTo(!d.personal) && (
                            <DropdownMenuItem
                              data-testid="dashboard-copy"
                              onClick={() => duplicateMut.mutate({ id: d.id, personal: !d.personal })}
                            >
                              {d.personal ? `Copy to ${ORG}` : `Copy to ${MINE}`}
                            </DropdownMenuItem>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                    {canMove(d) && (
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
                    )}
                    {canMove(d) && (
                      <DeleteButton
                        size="icon-sm"
                        title="Delete dashboard"
                        aria-label="Delete dashboard"
                        onClick={() => setDeleteTarget(d)}
                      />
                    )}
                  </>}
                />
              )
            }}
          </DashboardCardGrid>
        </>
      )}
      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={open => { if (!open) setDeleteTarget(null) }}
        title="Delete dashboard?"
        description={
          deleteTarget?.personal
            ? `Delete dashboard "${deleteTarget.name}" and all its charts?`
            : `Delete the organization dashboard "${deleteTarget?.name ?? ''}" and all its charts? Everyone in the organization will lose it.`
        }
        actionLabel="Delete"
        variant="destructive"
        testId="dashboard-delete-confirm"
        onConfirm={() => { if (deleteTarget) deleteMut.mutate(deleteTarget.id) }}
      />
    </PageContainer>
  )
}
