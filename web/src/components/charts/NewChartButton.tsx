// The two "New …" buttons on the dashboard pages.
//
// Both open a dialog, as a "New …" button does. Describing a chart in words
// is the chat's job (create_chart), which the builder links to.
import { useState } from 'react'
import { Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useGuideReveal } from '@/lib/guide-reveal'
import { CreateDashboardDialog } from './CreateDashboardDialog'
import { ChartBuilderDialog } from './ChartBuilderDialog'

export function NewDashboardButton({ personal, canShare }: {
  /** The tab it was pressed on — the side the new dashboard starts on. */
  personal: boolean
  canShare: boolean
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button data-testid="new-dashboard" onClick={() => setOpen(true)}>
        <Plus /> New dashboard
      </Button>
      <CreateDashboardDialog open={open} onClose={() => setOpen(false)} personal={personal} canShare={canShare} />
    </>
  )
}

/** Tour anchors inside the chart builder: a step there opens it. */
const CHART_BUILDER_ANCHORS = ['chart_builder.metrics', 'chart_builder.options'] as const

export function NewChartButton({ dashboardId, dashboardName }: { dashboardId: number; dashboardName: string }) {
  const [open, setOpen] = useState(false)
  // Opened by a guided tour, which also closes it when it moves on.
  const [guided, setGuided] = useState(false)
  const close = () => { setOpen(false); setGuided(false) }
  useGuideReveal(CHART_BUILDER_ANCHORS, () => { setOpen(true); setGuided(true) }, close)
  return (
    <>
      <Button data-testid="new-chart" onClick={() => setOpen(true)}>
        <Plus /> New chart
      </Button>
      <ChartBuilderDialog open={open} onClose={close} dashboardId={dashboardId} dashboardName={dashboardName} guided={guided} />
    </>
  )
}
