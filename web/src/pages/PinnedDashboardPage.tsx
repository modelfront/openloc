// One pinned dashboard: the person's own arrangement of charts that live on
// other dashboards. Its name, filter row and card order are theirs alone. A
// chart opens on its own page under its original dashboard, so an edit there
// changes the original, for whoever may edit it.
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { FolderInput } from 'lucide-react'
import {
  getPinnedDashboard, listPinnedDashboards, renamePinnedDashboard, updatePinnedFilters,
  reorderPinnedCharts, movePinnedChart, unpinChart, type PinnedChart,
  type DashboardFilterValues, type DashboardFilterOptions,
} from '../api'
import { FILTER_DEFAULTS } from '@/components/charts/DashboardFilterBar'
import { ConnectedFilterBar, useFilterValues, toSearchParams, useFilterOptions, needsOptions, CONTROL_FIELD } from '@/components/charts/DashboardFilters'
import { FilterEditMenu } from '@/components/charts/FilterEditMenu'
import { ChartCard } from '@/components/charts/ChartCard'
import { ChartGrid } from '@/components/charts/ChartGrid'
import { PinButton } from '@/components/charts/PinButton'
import { DASHBOARDS_PATH, PINNED_DASHBOARDS_PATH } from '@/components/charts/dashboard-paths'
import { EditableTitle } from '@/components/list/EditableTitle'
import { hoverActionClass } from '@/components/list/hover-action'
import { Button } from '@/components/ui/button'
import { TextLink } from '@/components/ui/text-link'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuGroup } from '@/components/ui/dropdown-menu'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { usePageTitle } from '../hooks/usePageTitle'

export function PinnedDashboardPage() {
  const { pinnedId } = useParams()
  const id = Number(pinnedId)
  const [filters, setFilters] = useFilterValues()
  const [renaming, setRenaming] = useState(false)
  const navigate = useNavigate()
  const { data: pinned, isLoading, isError } = useQuery({
    queryKey: queryKeys.dashboards.pinnedDetail(id),
    queryFn: () => getPinnedDashboard(id),
    enabled: Number.isFinite(id) && id > 0,
  })
  usePageTitle(pinned?.name)

  const filtersMut = useMutationWithToast({
    mutationFn: (controls: string[]) => updatePinnedFilters(id, { controls }),
    successMessage: 'Filters saved',
    errorMessage: 'Failed to save the filters',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const renameMut = useMutationWithToast({
    mutationFn: (name: string) => renamePinnedDashboard(id, name),
    successMessage: 'Dashboard renamed',
    errorMessage: 'Failed to rename the dashboard',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const reorderMut = useMutationWithToast({
    mutationFn: (ids: number[]) => reorderPinnedCharts(id, ids),
    successMessage: 'Order saved',
    errorMessage: 'Failed to save the order',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const options = useFilterOptions(
    id,
    filters.last_days ?? FILTER_DEFAULTS.last_days,
    needsOptions([
      ...(pinned?.filters?.controls ?? []),
      ...(pinned?.charts ?? []).flatMap(c => c.controls?.controls ?? []),
    ]),
    true,
  )

  if (isLoading) return <LoadingPlaceholder />
  if (isError || !pinned) {
    return (
      <PageContainer>
        <EmptyState message="Pinned dashboard not found." />
      </PageContainer>
    )
  }

  const controls = pinned.filters?.controls ?? []

  return (
    <PageContainer width="wide">
      <Breadcrumbs items={[
        { label: 'Dashboards', to: DASHBOARDS_PATH },
        { label: 'Pinned', to: PINNED_DASHBOARDS_PATH },
        {
          label: (
            <span className="inline-flex items-center gap-1">
              <EditableTitle
                name={pinned.name}
                onRename={name => renameMut.mutate(name)}
                editing={renaming}
                onEditingChange={setRenaming}
              />
              {!renaming && (
                <FilterEditMenu
                  label="dashboard"
                  testId="rename-dashboard"
                  onRename={() => setRenaming(true)}
                  available={pinned.available_controls}
                  active={controls}
                  values={filters}
                  // The charts are not this dashboard's, so a removed control
                  // freezes nothing: its selection just leaves the URL.
                  freezes={false}
                  onAdd={control => filtersMut.mutate([...controls, control])}
                  onRemove={control => {
                    setFilters({ [CONTROL_FIELD[control] ?? control]: undefined })
                    filtersMut.mutate(controls.filter(c => c !== control))
                  }}
                />
              )}
            </span>
          ),
        },
      ]} />
      <ConnectedFilterBar controls={controls} values={filters} options={options} onChange={setFilters} />
      <ChartGrid
        charts={pinned.charts}
        canArrange
        onReorder={ids => reorderMut.mutate(ids)}
        emptyMessage="No charts pinned here."
      >
        {chart => (
          <PinnedChartCard
            chart={chart}
            pinnedId={pinned.id}
            href={`/dashboards/${chart.dashboard_id}/charts/${chart.id}?${toSearchParams(filters)}`}
            filters={filters}
            options={options}
            alignFilterRow={pinned.charts.some(c => (c.controls?.controls ?? []).length > 0)}
            // The server removes a pinned dashboard with its last chart, so
            // the page goes where that chart went.
            onLastLeft={pinned.charts.length === 1
              ? to => navigate(to ? `${PINNED_DASHBOARDS_PATH}/${to}` : PINNED_DASHBOARDS_PATH, { replace: true })
              : undefined}
          />
        )}
      </ChartGrid>
    </PageContainer>
  )
}

function PinnedChartCard({ chart, pinnedId, onLastLeft, ...card }: {
  chart: PinnedChart
  pinnedId: number
  href: string
  filters: DashboardFilterValues
  options?: DashboardFilterOptions
  alignFilterRow: boolean
  /** Set when this is the dashboard's only chart: called once it is unpinned
   *  (no argument) or moved (the pinned dashboard it went to). */
  onLastLeft?: (to?: number) => void
}) {
  const unpinMut = useMutationWithToast({
    mutationFn: () => unpinChart(chart.id),
    successMessage: 'Chart unpinned',
    errorMessage: 'Failed to update the pin',
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: () => onLastLeft?.(),
  })
  return (
    <ChartCard
      chart={chart}
      {...card}
      pin={<PinButton pinned subject={chart.title} loading={unpinMut.isPending} onToggle={() => unpinMut.mutate()} />}
      source={
        <p className="text-xs text-gray-500 dark:text-gray-400" data-testid="pinned-chart-source">
          <TextLink tone="muted" to={`/dashboards/${chart.dashboard_id}`}>{chart.dashboard_name}</TextLink>
        </p>
      }
      actions={<MovePinnedChartMenu chartId={chart.id} chartTitle={chart.title} pinnedId={pinnedId} onMoved={onLastLeft} />}
    />
  )
}

/** Moves a pinned chart to another of the person's pinned dashboards. */
function MovePinnedChartMenu({ chartId, chartTitle, pinnedId, onMoved }: {
  chartId: number
  chartTitle: string
  pinnedId: number
  onMoved?: (to: number) => void
}) {
  const { data: pinned } = useQuery({ queryKey: queryKeys.dashboards.pinned, queryFn: listPinnedDashboards })
  const moveMut = useMutationWithToast({
    mutationFn: (to: number) => movePinnedChart(chartId, to).then(() => to),
    successMessage: 'Chart moved',
    errorMessage: 'Failed to move the chart',
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: to => onMoved?.(to),
  })
  const targets = (pinned ?? []).filter(p => p.id !== pinnedId)
  if (targets.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            className={hoverActionClass('muted')}
            title="Move to another pinned dashboard"
            aria-label={`Move ${chartTitle}`}
            data-testid="pinned-chart-move"
            disabled={moveMut.isPending}
          />
        }
      >
        <FolderInput className="h-4 w-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Move to</DropdownMenuLabel>
          {targets.map(p => (
            <DropdownMenuItem key={p.id} onClick={() => moveMut.mutate(p.id)} data-testid={`pinned-chart-move-to-${p.id}`}>
              <span className="truncate">{p.name}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
