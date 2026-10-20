// One live chart on a dashboard grid. The card fetches its own data endpoint,
// so one broken chart degrades to an error card instead of blanking the page.
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getChartData, chartDataQuery, type DashboardChart, type DashboardFilterValues, type DashboardFilterOptions } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { chartSpecFor, describeFilters } from './DashboardFilterBar'
import { ExportChartButton } from './ExportChartButton'
import { ChartResultNote } from './ChartResultNote'
import { useChartFilters } from './DashboardFilters'
import { ChartFilterBar } from './ChartFilterBar'
import { ChartArea } from './ChartArea'

export function ChartCard({ chart, href, filters, options, alignFilterRow, pin, actions, source }: {
  chart: DashboardChart
  /** The chart's own page, opened from the title. */
  href: string
  /** The DASHBOARD's selections; this chart's own are merged over them. */
  filters: DashboardFilterValues
  /** Dimension values with data in the window, shared with the dashboard bar. */
  options?: DashboardFilterOptions
  /** Some card on this dashboard has its own filters, so every card reserves
   *  the row to keep the charts on one line. */
  alignFilterRow: boolean
  /** The pin, just before the title. */
  pin?: ReactNode
  /** Hover actions after the export button. */
  actions?: ReactNode
  /** A line under the title, e.g. the dashboard a pinned chart lives on. */
  source?: ReactNode
}) {
  // The dashboard's selections, with this chart's own merged over them. Its
  // request stays flat — the namespacing exists only in the browser URL.
  const controls = chart.controls?.controls ?? []
  const [effective, own, setOwn] = useChartFilters(chart.id, controls, filters)
  const query = chartDataQuery(effective)
  // The cache key carries the same string the request does, so changing a
  // dropdown can never leave the card showing a cached answer to another
  // question.
  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.dashboards.chartData(chart.id, query),
    queryFn: () => getChartData(chart.id, effective),
    // Numbers move slowly (aggregates over history); a minute of staleness is
    // fine and keeps tab-switching cheap.
    staleTime: 60_000,
  })

  return (
    <div className="group relative flex h-full flex-col rounded-lg border border-gray-200 p-3 dark:border-gray-800" data-testid="dashboard-chart-card">
      <div className="mb-1 flex items-start justify-between gap-2">
        <div>
          {/* The measure's human definition is a tooltip, not a subtitle: it
              explains what the number counts, which the title already names in
              user words. The card never states its window: a date-range
              filter shows it where there is one, and a note under every title
              is noise where there is not.

              The title is the link rather than the whole card: the chart body
              owns hover tooltips, and the grip owns the drag. It underlines
              whenever the card is hovered, so it reads as the way in. */}
          <span className="inline-flex items-center gap-1.5">
            {pin}
            <Link
              to={href}
              className="text-sm font-medium underline-offset-2 group-hover:underline"
              title={data?.definition || undefined}
              data-testid="chart-open-link"
            >
              {chart.title}
            </Link>
            <ChartResultNote
              truncated={data?.truncated}
              grain={data?.grain}
              requestedGrain={filters.grain}
            />
          </span>
          {source}
        </div>
        <div className="flex shrink-0 items-center gap-1 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 has-[[data-popup-open]]:opacity-100">
          <ExportChartButton
            title={chart.title}
            spec={data?.rows.length ? chartSpecFor(chart.viz, data, effective.unit) : undefined}
            definition={data?.definition}
            filters={describeFilters(
              // No date control on the dashboard means the chart ran over its
              // own stored window, so the file states that one.
              { ...effective, last_days: effective.last_days ?? chart.definition.last_days },
              chart.available_controls,
              chart.definition.filters,
            )}
          />
          {actions}
        </div>
      </div>
      <ChartFilterBar
        controls={controls}
        values={own}
        inherited={filters}
        options={options}
        onChange={setOwn}
        reserveSpace={alignFilterRow}
      />
      <ChartArea
        viz={chart.viz}
        data={data}
        isLoading={isLoading}
        error={error}
        unit={effective.unit}
        size="card"
        testId="dashboard-chart"
      />
    </div>
  )
}
