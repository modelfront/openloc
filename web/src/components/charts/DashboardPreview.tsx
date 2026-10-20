// A dashboard's first charts, drawn small on its card in the list. The layout
// is the dashboard's own two-column grid, two rows of it, so the card reads as
// a thumbnail of the page; slots past the last chart stay empty.
//
// Each chart asks its data endpoint under the dashboard's DEFAULT selection —
// the same request, and the same cache entry, as the dashboard page opened
// without filters. Nothing is fetched until the card is near the viewport.
import { useQuery } from '@tanstack/react-query'
import { getChartData, chartDataQuery, type DashboardPreviewChart } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useInView } from '@/hooks/useInView'
import { chartSpecFor, FILTER_DEFAULTS } from '@/components/charts/DashboardFilterBar'
import { SpecChart } from '@/components/charts/SpecChart'
import { EditableTitle } from '@/components/list/EditableTitle'

/** Tall enough to tell a trend from a spike, short enough for two rows. */
const TILE_CHART_HEIGHT = 72

const DEFAULTS = { ...FILTER_DEFAULTS }

export function DashboardPreview({ charts }: { charts: DashboardPreviewChart[] }) {
  const [ref, inView] = useInView<HTMLDivElement>()
  return (
    <div ref={ref} className="mt-3 grid grid-cols-2 grid-rows-2 gap-2" data-testid="dashboard-preview">
      {charts.length === 0 ? (
        <div className="col-span-2 row-span-2 flex items-center justify-center rounded-md border border-dashed border-gray-200 text-xs text-gray-400 dark:border-gray-800">
          No charts yet
        </div>
      ) : (
        charts.map(c => <PreviewTile key={c.id} chart={c} enabled={inView} />)
      )}
    </div>
  )
}

function PreviewTile({ chart, enabled }: { chart: DashboardPreviewChart; enabled: boolean }) {
  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.dashboards.chartData(chart.id, chartDataQuery(DEFAULTS)),
    queryFn: () => getChartData(chart.id, DEFAULTS),
    staleTime: 60_000,
    enabled,
  })
  const note = !enabled || isLoading ? '' : error ? 'Could not load' : !data?.rows.length ? 'No data' : null
  return (
    <div className="min-w-0 rounded-md border border-gray-200 px-2 pb-1 pt-1.5 dark:border-gray-800" data-testid="dashboard-preview-tile">
      <p className="flex min-w-0 text-[11px] text-gray-500">
        <EditableTitle name={chart.title} canEdit={false} />
      </p>
      {note === null && data ? (
        <SpecChart compact testId="dashboard-preview-chart" height={TILE_CHART_HEIGHT} spec={chartSpecFor(chart.viz, data, DEFAULTS.unit)} />
      ) : (
        <div className="flex items-center justify-center text-[11px] text-gray-400" style={{ height: TILE_CHART_HEIGHT }}>
          {note}
        </div>
      )}
    </div>
  )
}
