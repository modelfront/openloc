// A chart opened by SHARE TOKEN.
//
// The reader may be someone who cannot browse dashboards at all — a linguist,
// or a colleague who was sent a link to a private chart. So this page shows the
// chart and nothing that implies a way in: no breadcrumbs, no rename, no
// delete, no link to the dashboard. The dashboard's NAME is shown as context,
// because a chart with no provenance is hard to trust.
//
// The filters still work. The link pins the sender's selection, and the reader
// can move the controls from there — it is their copy of the view, and changing
// it writes nothing.
import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getSharedChart, getSharedChartData, chartDataQuery } from '../api'
import { DashboardFilterBar, chartSpecFor, describeFilters } from '@/components/charts/DashboardFilterBar'
import { ExportChartButton } from '@/components/charts/ExportChartButton'
import { hoverActionClass } from '@/components/list/hover-action'
import { ChartResultNote } from '@/components/charts/ChartResultNote'
import { useFilterValues, toSearchParams } from '@/components/charts/DashboardFilters'
import { CopyChartLink } from '@/components/charts/ShareChartButton'
import { queryKeys } from '../lib/query-keys'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { ChartArea } from '@/components/charts/ChartArea'

const ACTION = hoverActionClass('muted')

export function SharedChartPage() {
  const { token = '' } = useParams()
  const [filters, setFilters] = useFilterValues()

  const { data: chart, isLoading, isError } = useQuery({
    queryKey: queryKeys.dashboards.sharedChart(token),
    queryFn: () => getSharedChart(token),
    enabled: token !== '',
  })
  const query = chartDataQuery(filters)
  const { data, isLoading: dataLoading, error } = useQuery({
    queryKey: queryKeys.dashboards.sharedChartData(token, query),
    queryFn: () => getSharedChartData(token, filters),
    enabled: !!chart,
    staleTime: 60_000,
  })

  if (isLoading) return <LoadingPlaceholder />
  // Revoked, mistyped, or issued by another organisation — all one 404, so a
  // token cannot be probed for which kind of wrong it is.
  if (isError || !chart) {
    return (
      <PageContainer>
        <EmptyState message="This link is no longer available. Ask whoever sent it for a new one." />
      </PageContainer>
    )
  }

  const controls = chart.filters?.controls ?? []
  return (
    <PageContainer width="wide">
      {/* Shared, not owned: the subtitle says where it came from without
          offering a way in. Dimension dropdowns are omitted along with their
          options endpoint, which is dashboard-scoped and would 403 here. */}
      <PageHeader title={chart.title} subtitle={`Shared from ${chart.dashboard}`} />
      {/* The chart's actions sit where they do on the dashboard's own chart
          page: at the end of the filter row. Share copies this link — the
          reader holds no right to mint another. */}
      <div className="flex items-end justify-between gap-3">
        <DashboardFilterBar
          controls={controls.filter(c => ['date_range', 'unit', 'granularity'].includes(c))}
          values={filters}
          onChange={setFilters}
        />
        <div className="mb-4 ml-auto flex shrink-0 items-center gap-1">
          <ExportChartButton
            size="icon"
            title={chart.title}
            spec={data?.rows.length ? chartSpecFor(chart.viz, data, filters.unit) : undefined}
            definition={data?.definition}
            filters={describeFilters(
              { ...filters, last_days: filters.last_days ?? chart.definition.last_days },
              chart.available_controls ?? [],
              chart.definition.filters,
            )}
          />
          <CopyChartLink search={toSearchParams(filters).toString()} className={ACTION} />
        </div>
      </div>
      <div className="rounded-lg border border-gray-200 p-4 dark:border-gray-800" data-testid="shared-chart">
        <div className="mb-3 flex items-start gap-1.5">
          {data?.definition && <p className="text-sm text-gray-500">{data.definition}</p>}
          <ChartResultNote truncated={data?.truncated} grain={data?.grain} requestedGrain={filters.grain} />
        </div>
        <ChartArea
          viz={chart.viz}
          data={data}
          isLoading={dataLoading}
          error={error}
          unit={filters.unit}
          size="page"
          testId="shared-chart-chart"
        />
      </div>
    </PageContainer>
  )
}
