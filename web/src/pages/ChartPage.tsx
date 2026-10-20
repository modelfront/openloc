// One chart, full page. Reached by clicking a
// card on a dashboard; its URL is the share unit.
//
// There is no chart endpoint behind this: the dashboard read already returns
// every chart the caller may see, along with the filter controls, so this page
// reuses that one query — and inherits its visibility rule (a personal
// dashboard's charts are its owner's alone) without a second place to get it
// wrong.
import { useParams } from 'react-router-dom'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { getDashboard, getChartData, chartDataQuery, renameChart, shareChart, getMe, type ChartData, type DashboardChart, type DashboardFilterValues } from '../api'
import { chartSpecFor, describeFilters, FILTER_DEFAULTS } from '@/components/charts/DashboardFilterBar'
import { ExportChartButton } from '@/components/charts/ExportChartButton'
import { hoverActionClass } from '@/components/list/hover-action'
import { ChartResultNote } from '@/components/charts/ChartResultNote'
import { ConnectedFilterBar, useFilterValues, toSearchParams, dashboardCrumbs, useFilterOptions, needsOptions } from '@/components/charts/DashboardFilters'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { isAdminRole } from '../lib/roles'
import { EditableTitle } from '@/components/list/EditableTitle'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { ChartArea } from '@/components/charts/ChartArea'
import { ShareChartButton } from '@/components/charts/ShareChartButton'
import { EditChartButton } from '@/components/charts/EditChartButton'
import { usePageTitle } from '../hooks/usePageTitle'
import { usePins } from '../hooks/usePins'
import { PinButton } from '@/components/charts/PinButton'

const ACTION = hoverActionClass('muted')

export function ChartPage() {
  const { dashboardId, chartId } = useParams()
  const id = Number(dashboardId)
  const cid = Number(chartId)
  const [filters, setFilters] = useFilterValues()

  const { data: dashboard, isLoading, isError } = useQuery({
    queryKey: queryKeys.dashboards.detail(id),
    queryFn: () => getDashboard(id),
    enabled: Number.isFinite(id) && id > 0,
  })
  const chart = dashboard?.charts.find(c => c.id === cid)
  usePageTitle(chart?.title, dashboard?.name)
  const allControls = [...(dashboard?.filters?.controls ?? []), ...(chart?.controls?.controls ?? [])]
  const options = useFilterOptions(id, filters.last_days ?? FILTER_DEFAULTS.last_days, needsOptions(allControls))
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const canWrite = !!dashboard && (dashboard.personal || isAdminRole(me?.role))
  const pins = usePins()
  const renameMut = useMutationWithToast({
    mutationFn: (name: string) => renameChart(cid, name),
    successMessage: 'Chart renamed',
    errorMessage: 'Failed to rename the chart',
    invalidateKeys: [queryKeys.dashboards.all],
  })

  // Read at the page, not the body: the download in the action row and the
  // chart below it are the same answer, so they share one query.
  const result = useQuery({
    queryKey: queryKeys.dashboards.chartData(cid, chartDataQuery(filters)),
    queryFn: () => getChartData(cid, filters),
    staleTime: 60_000,
    enabled: !!chart,
  })

  // The selection, spelled out — it drives both the crumb back to the dashboard
  // and the share link. A bare URL would mean "whatever the defaults are the
  // day you open it" rather than what the sender is looking at.
  const backSearch = toSearchParams(filters).toString()

  if (isLoading) return <LoadingPlaceholder />
  // One message for "deleted", "wrong id" and "someone else's personal
  // dashboard": the read cannot tell them apart on purpose, so that a chart id
  // cannot be probed. It still has to say why a link a colleague sent might
  // not open.
  if (isError || !dashboard || !chart) {
    return (
      <PageContainer>
        <EmptyState message="Chart not found. It may have been deleted, or it lives on a personal dashboard that only its owner can open." />
      </PageContainer>
    )
  }

  return (
    <PageContainer width="wide">
      {/* Three levels, and the middle one keeps the selection: stepping back up
          to the dashboard must not silently widen the window you came from. */}
      <Breadcrumbs items={[
          ...dashboardCrumbs(dashboard.personal),
          { label: dashboard.name, to: `/dashboards/${id}${backSearch ? `?${backSearch}` : ''}` },
          {
            label: (
              // Rename only. This page shows the DASHBOARD's controls merged
              // with the chart's, so an "edit filters" menu here could not say
              // which of the two it was editing — the card, where only the
              // chart's own row is in view, is where that belongs.
              <span className="inline-flex items-center gap-1">
                <PinButton {...pins.chart(cid)} subject={chart.title} />
                <EditableTitle
                  name={chart.title}
                  onRename={name => renameMut.mutate(name)}
                  // The chart's gate is its dashboard's: retitling one on a
                  // shared dashboard changes what the whole org reads.
                  canEdit={canWrite}
                  data-testid="rename-chart"
                />
              </span>
            ),
          },
        ]} />
      {/* The breadcrumb keeps its own row. The filters share the one below it
          with the chart's actions — download, share, edit, as on a card.
          The single-chart page merges the dashboard's controls with this
          chart's own into one row: there is only one chart here, so the
          distinction has nothing to express. */}
      <div className="flex items-end justify-between gap-3">
        <ConnectedFilterBar
          controls={[...(dashboard.filters?.controls ?? []), ...(chart.controls?.controls ?? [])]}
          values={filters}
          options={options}
          onChange={setFilters}
        />
        <div className="mb-4 ml-auto flex shrink-0 items-center gap-1" data-guide="dashboard_chart.actions">
          <ExportChartButton
            size="icon"
            title={chart.title}
            spec={result.data?.rows.length ? chartSpecFor(chart.viz, result.data, filters.unit) : undefined}
            definition={result.data?.definition}
            filters={describeFilters(
              { ...filters, last_days: filters.last_days ?? chart.definition.last_days },
              chart.available_controls,
              chart.definition.filters,
            )}
          />
          {/* Share is for every reader; only a writer's creates a link that
              opens for colleagues who cannot reach the dashboard. */}
          <ShareChartButton chartId={cid} onShare={shareChart} search={backSearch} canMint={canWrite} className={ACTION} />
          {canWrite && <EditChartButton dashboardId={id} chart={chart} size="icon" />}
        </div>
      </div>
      <ChartBody chart={chart} filters={filters} result={result} />
    </PageContainer>
  )
}

function ChartBody({ chart, filters, result }: {
  chart: DashboardChart
  filters: DashboardFilterValues
  result: Pick<UseQueryResult<ChartData>, 'data' | 'isLoading' | 'error'>
}) {
  const { data, isLoading, error } = result

  return (
    <div className="rounded-lg border border-gray-200 p-4 dark:border-gray-800" data-testid="chart-detail" data-guide="dashboard_chart.chart">
      {/* The measure's definition earns a visible line here rather than the
          card's tooltip: a reader who arrived by link has none of the context
          the dashboard gave. */}
      <div className="mb-3 flex items-start gap-1.5">
        {data?.definition && <p className="text-sm text-gray-500">{data.definition}</p>}
        <ChartResultNote truncated={data?.truncated} grain={data?.grain} requestedGrain={filters.grain} />
      </div>
      <ChartArea
        viz={chart.viz}
        data={data}
        isLoading={isLoading}
        error={error}
        unit={filters.unit}
        size="page"
        testId="chart-detail-chart"
      />
    </div>
  )
}
