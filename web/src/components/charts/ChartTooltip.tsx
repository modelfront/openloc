// The box recharts shows while the pointer is over a chart: the x value (a
// date range, a category), one row per series at that point (a pie: one per
// slice, with its share), and a total when the rows add up. The row of the
// band under the pointer, or of the legend entry being hovered, is bold. A
// table, so names, values and shares line up in columns.
import { formatAxisNumber } from '@/lib/chart-axis-format'
import { formatShare, withUnit } from '@/lib/chart-legend'
import { toNumber } from '@/lib/chart-pivot'
import { cn } from '@/lib/utils'

/** One row as recharts hands it over, plus a pie slice's share. */
export interface TooltipRow {
  name?: string | number
  value?: unknown
  color?: string
  payload?: { fill?: string }
  /** The slice's share of what is shown; rows that have one get a share column. */
  share?: number | null
}

export function ChartTooltip({ active, payload, label, formatLabel, highlighted, unitOf, additive }: {
  /** recharts: whether the pointer is over the chart. */
  active?: boolean
  payload?: readonly TooltipRow[]
  label?: unknown
  formatLabel?: (label: unknown) => string
  /** The series or slice to set in bold. */
  highlighted: string | null
  unitOf: (name: string) => string | undefined
  /** The rows are parts of one whole: a total is shown when they also share one known unit. */
  additive: boolean
}) {
  // A gap (an average with no value that week) is no row, not a 0.
  const rows = payload?.filter(row => row.value != null) ?? []
  if (!active || !rows.length) return null
  const units = new Set(rows.map(row => unitOf(String(row.name ?? ''))))
  const total = additive && rows.length > 1 && units.size === 1 && !units.has(undefined)
    ? rows.reduce((sum, row) => sum + toNumber(row.value), 0)
    : null
  const shares = rows.some(row => row.share !== undefined)
  const heading = label == null || label === '' ? null : formatLabel ? formatLabel(label) : String(label)
  return (
    <div
      className="rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md"
      data-testid="chart-tooltip"
    >
      {heading && <p className="mb-1 font-medium">{heading}</p>}
      <table className="border-collapse">
        <tbody>
          {rows.map(row => {
            const name = String(row.name ?? '')
            const value = toNumber(row.value)
            // A series named after its column ("words") would read "words 400 words".
            const unit = unitOf(name)
            return (
              <tr
                key={name}
                className={cn(name === highlighted && 'font-semibold')}
                data-testid="chart-tooltip-row"
                data-highlighted={name === highlighted || undefined}
              >
                <td className="py-px pr-3">
                  <span className="flex items-center gap-1.5">
                    <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ backgroundColor: row.color ?? row.payload?.fill }} />
                    {name}
                  </span>
                </td>
                <td className="py-px text-right tabular-nums">{withUnit(formatAxisNumber(value), value, unit === name ? undefined : unit)}</td>
                {shares && <td className="py-px pl-3 text-right tabular-nums">{row.share == null ? '' : formatShare(row.share)}</td>}
              </tr>
            )
          })}
        </tbody>
        {total != null && (
          <tfoot>
            <tr className="border-t border-border font-medium" data-testid="chart-tooltip-total">
              <td className="pt-1 pr-3">Total</td>
              <td className="pt-1 text-right tabular-nums">{withUnit(formatAxisNumber(total), total, [...units][0])}</td>
              {shares && <td className="pt-1 pl-3" />}
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  )
}
