// The body of a chart wherever one is drawn — a dashboard card, the chart's
// own page, a shared link: loading, a failed query, an empty period, or the
// chart. One component so the three states read the same everywhere.
//
// A failed query includes a stale definition (a 422 naming the retired
// measure): the chart explains itself instead of blanking the page around it.
import type { ChartData, DashboardChart } from '@/api'
import { chartSpecFor } from '@/components/charts/DashboardFilterBar'
import { SpecChart } from '@/components/charts/SpecChart'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { cn } from '@/lib/utils'

// A card's states grow to fill the card, whose row is as tall as its tallest
// neighbour; a page gives the chart a fixed, roughly double-card height.
const SIZES = {
  card: { height: 240, box: 'min-h-[240px] flex-1' },
  page: { height: 480, box: 'h-[480px]' },
} as const

export function ChartArea({ viz, data, isLoading, error, unit, size, testId }: {
  viz: DashboardChart['viz']
  data?: ChartData
  isLoading: boolean
  error: unknown
  unit?: string
  size: keyof typeof SIZES
  /** On the drawn chart, for tests that wait for it. */
  testId: string
}) {
  const { height, box } = SIZES[size]
  const frame = cn('flex items-center justify-center text-sm', box)
  if (isLoading) {
    return <div className={frame}><LoadingPlaceholder size="sm" delayMs={0} /></div>
  }
  if (error) {
    return (
      <div className={cn(frame, 'px-4')} data-testid="chart-error">
        <LoadFailed what="chart" error={error} />
      </div>
    )
  }
  if (!data?.rows.length) {
    return <div className={cn(frame, 'text-gray-400')} data-testid="chart-empty">No data in this period yet.</div>
  }
  return <SpecChart testId={testId} height={height} spec={chartSpecFor(viz, data, unit)} />
}
