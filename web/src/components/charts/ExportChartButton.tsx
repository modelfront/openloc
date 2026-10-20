// Download a chart as a spreadsheet.
//
// Built in the browser from the rows the chart already holds, so the file is
// the chart on screen under the current selection — no second query that could
// come back different, and nothing a reader who can see the chart could not
// already see. That is also why there is no role gate: a linguist opening a
// shared link may download what that link shows them.
import { useState } from 'react'
import { Download } from 'lucide-react'
import { toast } from 'sonner'
import { triggerDownload, type ChartSpec } from '@/api'
import { chartTable, chartWorkbook, exportFileName } from '@/lib/chart-export'
import { hoverActionClass } from '@/components/list/hover-action'
import { Button } from '@/components/ui/button'

export function ExportChartButton({ title, spec, definition, filters, size = 'icon-sm' }: {
  title: string
  /** What SpecChart is drawing; undefined while there is nothing to export. */
  spec?: ChartSpec
  definition?: string
  /** The selection, in the filter bar's words (`describeFilters`). */
  filters: [string, string][]
  /** `icon-sm` in a card's action row, `icon` in a page header. */
  size?: 'icon' | 'icon-sm'
}) {
  const [busy, setBusy] = useState(false)
  const empty = !spec || spec.data.length === 0

  const download = async () => {
    if (!spec) return
    setBusy(true)
    const at = new Date()
    try {
      // A regular download, like every other file in the app: it lands in the
      // browser's downloads with nothing more to confirm.
      const blob = await chartWorkbook(chartTable(spec), { title, definition, filters, exportedAt: at })
      triggerDownload(blob, exportFileName(title, at))
    } catch (err) {
      toast.error(`Failed to export the chart: ${(err as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Button
      variant="ghost"
      size={size}
      className={hoverActionClass('muted')}
      title={empty ? 'Nothing to download in this period' : 'Download as a spreadsheet'}
      aria-label={`Download ${title} as a spreadsheet`}
      data-testid="chart-export"
      disabled={empty || busy}
      onClick={download}
    >
      <Download className="h-4 w-4" />
    </Button>
  )
}
