// Editing a chart is re-opening the form that made it, with its selections in
// place — measure, drawing, axis, values and filters in one dialog. The caller
// decides who sees it: the gate is the chart's dashboard's.
import { useState } from 'react'
import { Pencil } from 'lucide-react'
import type { DashboardChart } from '@/api'
import { Button } from '@/components/ui/button'
import { hoverActionClass } from '@/components/list/hover-action'
import { ChartBuilderDialog } from '@/components/charts/ChartBuilderDialog'

export function EditChartButton({ dashboardId, chart, size = 'icon-sm' }: {
  dashboardId: number
  chart: DashboardChart
  /** `icon-sm` in a card's action row, `icon` in a page header. */
  size?: 'icon' | 'icon-sm'
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        variant="ghost"
        size={size}
        className={hoverActionClass('muted')}
        title="Edit chart"
        aria-label={`Edit ${chart.title}`}
        data-testid="chart-edit"
        onClick={() => setOpen(true)}
      >
        <Pencil className="h-4 w-4" />
      </Button>
      <ChartBuilderDialog open={open} onClose={() => setOpen(false)} dashboardId={dashboardId} chart={chart} />
    </>
  )
}
