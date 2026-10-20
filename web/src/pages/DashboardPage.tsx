// One dashboard: a grid of live chart cards. Each card
// fetches its own data endpoint — the stored definition re-runs through the
// metrics semantic layer on every load, so numbers are current and one broken
// chart degrades to an error card instead of blanking the page.
import { useParams } from 'react-router-dom'
import { useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  getDashboard, deleteChart, reorderCharts, getMe,
  renameDashboard, removeDashboardFilter, updateDashboardFilters,
  type DashboardChart, type DashboardFilterValues, type DashboardFilterOptions,
} from '../api'
import { FILTER_DEFAULTS } from '@/components/charts/DashboardFilterBar'
import { MoveCopyChartMenu } from '@/components/charts/MoveCopyChartMenu'
import { ConnectedFilterBar, useFilterValues, toSearchParams, dashboardCrumbs, useFilterOptions, needsOptions } from '@/components/charts/DashboardFilters'
import { FilterEditMenu } from '@/components/charts/FilterEditMenu'
import { ChartCard } from '@/components/charts/ChartCard'
import { ChartGrid } from '@/components/charts/ChartGrid'
import { PinButton } from '@/components/charts/PinButton'
import { EditableTitle } from '@/components/list/EditableTitle'
import { NewChartButton } from '@/components/charts/NewChartButton'
import { EditChartButton } from '@/components/charts/EditChartButton'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { usePins } from '../hooks/usePins'
import { isAdminRole } from '../lib/roles'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { DeleteButton } from '@/components/ui/delete-button'
import ConfirmDialog from '@/components/ConfirmDialog'
import { usePageTitle } from '../hooks/usePageTitle'

export function DashboardPage() {
  const { dashboardId } = useParams()
  const id = Number(dashboardId)
  const [filters, setFilters] = useFilterValues()
  // The edit MENU opens the title's rename, so the field's open state is owned
  // here rather than inside EditableTitle.
  const [renamingDashboard, setRenamingDashboard] = useState(false)
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const { data: dashboard, isLoading, isError } = useQuery({
    queryKey: queryKeys.dashboards.detail(id),
    queryFn: () => getDashboard(id),
    enabled: Number.isFinite(id) && id > 0,
  })
  usePageTitle(dashboard?.name)

  // Removing a control freezes its CURRENT value into the charts it reached, so
  // the cards keep showing what they show; only the ability to change it goes.
  const removeFilterMut = useMutationWithToast({
    mutationFn: ({ control, value }: { control: string; value: string }) =>
      removeDashboardFilter(id, control, value),
    successMessage: 'Filter removed',
    errorMessage: 'Failed to remove the filter',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  // Adding sends the whole list, because the endpoint REPLACES it — the same
  // shape the chat tool has.
  const addFilterMut = useMutationWithToast({
    mutationFn: (controls: string[]) => updateDashboardFilters(id, { controls }),
    successMessage: 'Filter added',
    errorMessage: 'Failed to add the filter',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  // The toast appends the server's message, so a 409 arrives as "… already
  // exists" rather than a generic failure.
  // Card order is the dashboard's, so a drag writes it for everyone who can
  // see it.
  const reorderMut = useMutationWithToast({
    mutationFn: (ids: number[]) => reorderCharts(id, ids),
    successMessage: 'Order saved',
    errorMessage: 'Failed to save the order',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const pins = usePins()
  const renameMut = useMutationWithToast({
    mutationFn: (name: string) => renameDashboard(id, name),
    successMessage: 'Dashboard renamed',
    errorMessage: 'Failed to rename the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })

  // One options fetch for the page: the endpoint already unions the dashboard's
  // controls with every chart's own, so per-card fetches would repeat it.
  // Declared with the other hooks — after the early returns below it would be a
  // conditional hook, and the page renders nothing at all.
  const options = useFilterOptions(
    id,
    filters.last_days ?? FILTER_DEFAULTS.last_days,
    needsOptions([
      ...(dashboard?.filters?.controls ?? []),
      ...(dashboard?.charts ?? []).flatMap(c => c.controls?.controls ?? []),
    ]),
  )

  if (isLoading) return <LoadingPlaceholder />
  if (isError || !dashboard) {
    return (
      <PageContainer>
        <EmptyState message="Dashboard not found." />
      </PageContainer>
    )
  }

  const canWrite = dashboard.personal || isAdminRole(me?.role)
  const dashControls = dashboard?.filters?.controls ?? []

  return (
    <PageContainer width="wide">
      <Breadcrumbs items={[
          ...dashboardCrumbs(dashboard.personal),
          {
            label: (
              <span className="inline-flex items-center gap-1">
                <PinButton {...pins.dashboard(dashboard.id)} subject={dashboard.name} />
                <EditableTitle
                  name={dashboard.name}
                  onRename={name => renameMut.mutate(name)}
                  canEdit={canWrite}
                  editing={renamingDashboard}
                  onEditingChange={setRenamingDashboard}
                />
                {canWrite && !renamingDashboard && (
                  <FilterEditMenu
                    label="dashboard"
                    testId="rename-dashboard"
                    onRename={() => setRenamingDashboard(true)}
                    available={dashboard.available_controls}
                    active={dashControls}
                    values={filters}
                    onAdd={control => addFilterMut.mutate([...dashControls, control])}
                    onRemove={(control, value) => removeFilterMut.mutate({ control, value })}
                  />
                )}
              </span>
            ),
          },
        ]} />
      {/* The breadcrumb keeps its own row; the filters and the page's action
          share the one below it, as on the other detail pages. */}
      <div className="flex items-end justify-between gap-3" data-guide="dashboard.filters">
        <ConnectedFilterBar controls={dashControls} values={filters} options={options} onChange={setFilters} />
        {canWrite && (
          <div className="mb-4 ml-auto shrink-0" data-guide="dashboard.new_chart">
            <NewChartButton dashboardId={dashboard.id} dashboardName={dashboard.name} />
          </div>
        )}
      </div>
      <ChartGrid
        charts={dashboard.charts}
        // Reordering is a write to the dashboard, gated like every other one.
        canArrange={canWrite}
        onReorder={ids => reorderMut.mutate(ids)}
        // A new dashboard starts empty, so this is the first thing its creator
        // sees. Only someone who may add a chart is pointed at the button.
        emptyMessage={canWrite ? 'No charts yet. Add one with “New chart”, or ask the assistant for it.' : 'No charts yet.'}
      >
        {chart => (
          <DashboardChartCard
            chart={chart}
            dashboardId={dashboard.id}
            filters={filters}
            options={options}
            alignFilterRow={dashboard.charts.some(c => (c.controls?.controls ?? []).length > 0)}
            canWrite={canWrite}
            shared={!dashboard.personal}
            pin={<PinButton {...pins.chart(chart.id)} subject={chart.title} />}
          />
        )}
      </ChartGrid>
    </PageContainer>
  )
}


// The dashboard's own actions on one card, after the shared ones.
function DashboardChartCard({ chart, dashboardId, filters, options, alignFilterRow, canWrite, shared, pin }: {
  chart: DashboardChart
  dashboardId: number
  filters: DashboardFilterValues
  options?: DashboardFilterOptions
  alignFilterRow: boolean
  canWrite: boolean
  /** Deleting from a shared dashboard removes it for the whole org — say so. */
  shared: boolean
  pin: ReactNode
}) {
  const deleteMut = useMutationWithToast({
    mutationFn: () => deleteChart(chart.id),
    successMessage: 'Chart deleted',
    errorMessage: 'Failed to delete chart',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  return (
    <ChartCard
      chart={chart}
      // The permalink carries what the dashboard is currently showing, so
      // opening one chart never silently changes the question being asked.
      href={`/dashboards/${dashboardId}/charts/${chart.id}?${toSearchParams(filters)}`}
      filters={filters}
      options={options}
      alignFilterRow={alignFilterRow}
      pin={pin}
      actions={<>
        {canWrite && <EditChartButton dashboardId={dashboardId} chart={chart} />}
        {/* Offered to anyone who can see the chart: COPY only needs a write
            on the destination. The menu decides whether MOVE is on offer. */}
        <MoveCopyChartMenu
          chartId={chart.id}
          chartTitle={chart.title}
          dashboardId={dashboardId}
          canWriteCurrent={canWrite}
        />
        {canWrite && (
          <DeleteButton
            size="icon-sm"
            title="Delete chart"
            aria-label="Delete chart"
            onClick={() => setConfirmingDelete(true)}
          />
        )}
        <ConfirmDialog
          open={confirmingDelete}
          onOpenChange={setConfirmingDelete}
          title="Delete chart?"
          description={
            shared
              ? `Delete "${chart.title}" from this organization dashboard? Everyone in the organization will lose it.`
              : `Delete chart "${chart.title}"?`
          }
          testId="chart-delete-confirm"
          onConfirm={() => deleteMut.mutate()}
        />
      </>}
    />
  )
}
