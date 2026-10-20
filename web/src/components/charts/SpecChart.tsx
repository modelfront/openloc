// Spec-driven chart renderer shared by the chat's inline render_chart tool
// and dashboard cards. Specs are
// validated server-side and rendered verbatim; the only transform is
// `pivotChart`, which widens long rows when the spec splits a dimension's
// values into series (`series_key`).
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { useMemo, useState } from 'react'
import type { ChartSpec } from '../../api'
import { ChartLegend, type LegendEntry } from './ChartLegend'
import { formatShare, orderBySize, seriesMean, seriesSum, sliceShares, toggleSelected, withUnit } from '../../lib/chart-legend'
import { ChartTooltip, type TooltipRow } from './ChartTooltip'
import { PointerSeries } from './PointerSeries'
import { pivotChart, labelCategories, capSlices, OTHER_SERIES, toNumber } from '../../lib/chart-pivot'
import { grainOf, formatTick, formatTooltipLabel, tickGapFor, formatAxisNumber } from '../../lib/chart-axis-format'

// Palette aligned with the app accent first, then distinguishable hues that
// hold up in both themes. Sized to MAX_SERIES: a `series_key` split draws one
// band per dimension value, and a repeated colour is the exact confusion this
// palette has to prevent.
const COLORS = ['#6366f1', '#10b981', '#f59e0b', '#ef4444', '#0ea5e9', '#a855f7', '#14b8a6', '#f43f5e']

const AXIS_TICK = { fontSize: 11 } as const

// The tooltip stacks above whatever sits near the chart, the legend included.
const TOOLTIP_WRAPPER = { zIndex: 10 } as const

/**
 * `compact` draws the shape alone — no axes, grid, legend or tooltip, no frame —
 * for a preview too small to read values from. It takes no pointer events, so
 * a click lands on whatever the preview sits in.
 */
export function SpecChart({ spec, testId = 'spec-chart', height = 220, compact = false }: {
  spec: ChartSpec
  testId?: string
  height?: number
  compact?: boolean
}) {
  if (compact) {
    return (
      <div className="pointer-events-none" data-testid={testId}>
        <ResponsiveContainer width="100%" height={height}>
          {renderChart(spec, chartModel(spec), STATIC_VIEW, true)}
        </ResponsiveContainer>
      </div>
    )
  }
  return (
    <div className="my-2 rounded-lg border border-gray-200 p-2 dark:border-gray-800" data-testid={testId}>
      {spec.title && (
        <p className="mb-1 px-1 text-xs font-medium text-gray-600 dark:text-gray-300">{spec.title}</p>
      )}
      <FullChart spec={spec} height={height} />
    </div>
  )
}

/**
 * What the reader has done to the chart: the entries hidden in the legend, and
 * the one highlighted — by the legend, or by the pointer over its band.
 */
interface ChartView {
  hidden: ReadonlySet<string>
  active: string | null
  onHover?: (name: string | null) => void
}

const STATIC_VIEW: ChartView = { hidden: new Set(), active: null }

// A highlighted entry stays as drawn; the others fade to this.
const FADED = 0.25

// The chart and its legend. The legend's state lives here, for this card only:
// nothing is saved, and a reload shows everything again.
function FullChart({ spec, height }: { spec: ChartSpec; height: number }) {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set())
  const [active, setActive] = useState<string | null>(null)
  const model = useMemo(() => chartModel(spec), [spec])
  const view = { hidden, active, onHover: setActive }
  const entries = legendEntries(spec, model, view)
  return (
    <>
      <ResponsiveContainer width="100%" height={height}>
        {renderChart(spec, model, view)}
      </ResponsiveContainer>
      {entries && (
        <ChartLegend
          entries={entries}
          onHighlight={setActive}
          onToggle={name => {
            const next = toggleSelected(hidden, name, entries.map(e => e.name))
            // The entry under the pointer stays highlighted while it is shown.
            setActive(next.has(name) ? null : name)
            setHidden(next)
          }}
        />
      )}
    </>
  )
}

/** The rows and series a chart draws, shared by the chart and its legend. */
interface ChartModel {
  data: Record<string, unknown>[]
  series: string[]
  stacked: boolean
  grain: ReturnType<typeof grainOf>
  /** A pie's slices, empty dimension values named. */
  slices: Record<string, unknown>[]
}

function chartModel(spec: ChartSpec): ChartModel {
  // `series_key` widens the rows into one key per dimension value; without it
  // this is spec.data and spec.series unchanged.
  const pivoted = pivotChart(spec)
  const grain = grainOf(spec.x_key)
  // Biggest first in the legend, the stack and the colours alike: the first
  // colour is always the biggest band. Averages rank by their mean.
  const size = (name: string) => isAverage(spec, name) ? seriesMean(pivoted.data, name) : seriesSum(pivoted.data, name)
  const valueKey = spec.series[0]
  return {
    // A categorical x axis plots dimension values as ticks; a time axis has none empty.
    data: grain ? pivoted.data : labelCategories(pivoted.data, spec.x_key),
    series: orderBySize(pivoted.series, size, name => name === OTHER_SERIES),
    stacked: pivoted.stacked,
    grain,
    slices: spec.type === 'pie'
      ? capSlices(
          orderBySize(labelCategories(spec.data, spec.x_key), row => toNumber(row[valueKey])),
          spec.x_key, valueKey, !(spec.averages?.includes(valueKey) ?? false),
        )
      : [],
  }
}

/**
 * Whether these series (a pie: its slices) add up to a total and shares: none
 * is an average, every one's unit is known and the same, and none is the sum
 * of others shown beside it (total tokens next to input and output tokens).
 * A split and a pie plot one value column, so its rule decides for all.
 */
function addsUp(spec: ChartSpec, series: string[]): boolean {
  const columns = spec.type === 'pie' || spec.series_key ? [spec.series[0]] : series
  if (columns.some(c => spec.averages?.includes(c))) return false
  const units = new Set(columns.map(c => spec.units?.[c]))
  if (units.size !== 1 || units.has(undefined)) return false
  return columns.length < 2 || !columns.some(c => spec.derived?.includes(c))
}

// Whether a series holds averages: the split value column, or the column itself.
function isAverage(spec: ChartSpec, series: string): boolean {
  return spec.averages?.includes(spec.series_key ? spec.series[0] : series) ?? false
}

// Dimension bands always get a legend — with one band you still need to know
// WHICH value it is, unlike a lone value column whose name is the axis.
function hasLegend(spec: ChartSpec, model: ChartModel): boolean {
  return spec.type === 'pie' || model.series.length > 1 || !!spec.series_key
}

/**
 * The legend's entries, or null for a chart without one. A slice's tooltip
 * shows its value; a series' tooltip shows its total, unless its values are
 * averages, which do not add up. A pie's shares are in the chart's tooltip.
 */
function legendEntries(spec: ChartSpec, model: ChartModel, view: ChartView): LegendEntry[] | null {
  if (!hasLegend(spec, model)) return null
  if (spec.type === 'pie') {
    const valueKey = spec.series[0]
    const shares = sliceShares(model.slices, spec.x_key, valueKey, view.hidden)
    return model.slices.map((row, i) => {
      const name = String(row[spec.x_key])
      return {
        name, color: COLORS[i % COLORS.length], hidden: view.hidden.has(name),
        tooltip: withShare(compactWithUnit(toNumber(row[valueKey]), unitOf(spec, name)), shares.get(name)),
      }
    })
  }
  // A series' share of everything shown, when the shown series add up.
  const sums = new Map(model.series.map(name => [name, seriesSum(model.data, name)]))
  const shown = model.series.filter(name => !view.hidden.has(name))
  const addUp = addsUp(spec, shown)
  const whole = shown.reduce((sum, name) => sum + (sums.get(name) ?? 0), 0)
  return model.series.map((name, i) => ({
    name, color: COLORS[i % COLORS.length], hidden: view.hidden.has(name),
    tooltip: isAverage(spec, name) ? undefined : `Total: ${withShare(
      compactWithUnit(sums.get(name) ?? 0, unitOf(spec, name)),
      addUp && whole > 0 ? (sums.get(name) ?? 0) / whole : null,
    )}`,
  }))
}

// `929 words (85%)`; the value alone when there is no share to give.
function withShare(value: string, share: number | null | undefined): string {
  return share == null ? value : `${value} (${formatShare(share)})`
}

// What a series or slice counts: a split chart and a pie plot one value
// column, whose unit every entry shares; otherwise each series is a column.
function unitOf(spec: ChartSpec, name: string): string | undefined {
  return spec.units?.[spec.series_key || spec.type === 'pie' ? spec.series[0] : name]
}

function compactWithUnit(n: number, unit: string | undefined): string {
  return withUnit(formatAxisNumber(n), n, unit)
}

function opacityFor(view: ChartView, name: string): number {
  return view.active && view.active !== name ? FADED : 1
}

function renderChart(spec: ChartSpec, model: ChartModel, view: ChartView, compact = false) {
  const { data, series, stacked, grain } = model
  const stackId = stacked ? 'a' : undefined
  const common = compact
    ? { data, margin: { top: 2, right: 2, bottom: 2, left: 2 } }
    : { data, margin: { top: 4, right: 8, bottom: 0, left: 0 } }
  // A preview has nothing to read values from, and a dozen of them animating
  // at once is noise.
  const animate = !compact
  // A time axis gets readable dates; a categorical one is left exactly as the
  // server sent it. The axis stays short and the tooltip carries the year.
  const xAxis = grain
    ? {
        dataKey: spec.x_key,
        tick: AXIS_TICK,
        tickFormatter: (v: unknown) => formatTick(grain, v),
        // Thin the labels to whatever the width allows rather than printing one
        // per point: a dense weekly axis is unreadable, and a gap between dates
        // costs nothing because the tooltip still names every point.
        minTickGap: tickGapFor(grain),
      }
    : { dataKey: spec.x_key, tick: AXIS_TICK }
  // Sized to its widest label, so a long number is never cut off on the left.
  const yAxis = { tick: AXIS_TICK, width: 'auto' as const, tickFormatter: (v: unknown) => formatAxisNumber(v) }
  const formatLabel = grain ? (v: unknown) => formatTooltipLabel(grain, v) : undefined
  // A pie's rows carry each slice's share of the slices still shown.
  const pieShares = spec.type === 'pie' ? sliceShares(model.slices, spec.x_key, spec.series[0], view.hidden) : null
  const pieRows: TooltipRow[] = pieShares
    ? model.slices.flatMap((row, i) => {
        const name = String(row[spec.x_key])
        return view.hidden.has(name) ? [] : [{
          name, value: row[spec.series[0]], color: COLORS[i % COLORS.length], share: pieShares.get(name) ?? null,
        }]
      })
    : []
  const tooltip = {
    wrapperStyle: TOOLTIP_WRAPPER,
    content: (props: { active?: boolean; payload?: readonly TooltipRow[]; label?: unknown }) => (
      <ChartTooltip
        active={props.active}
        // recharts hands a pie's tooltip only the slice under the pointer; it
        // lists every shown slice instead, like the rows of a bar.
        payload={spec.type === 'pie' ? pieRows : props.payload}
        // A pie's rows name the slice; its label is only the slice again.
        label={spec.type === 'pie' ? undefined : props.label}
        formatLabel={formatLabel}
        highlighted={view.active ?? (spec.type === 'pie' ? String(props.payload?.[0]?.name ?? '') : null)}
        unitOf={name => unitOf(spec, name)}
        additive={addsUp(spec, series.filter(s => !view.hidden.has(s)))}
      />
    ),
  }
  // The pointer over a band highlights it, as hovering its legend entry does.
  // Bars and slices say so themselves; areas and lines go through PointerSeries.
  const shownSeries = series.filter(s => !view.hidden.has(s))
  const hover = (name: string) => ({
    onMouseEnter: () => view.onHover?.(name),
    onMouseLeave: () => view.onHover?.(null),
  })
  switch (spec.type) {
    case 'line':
      return (
        <LineChart {...common}>
          {!compact && <CartesianGrid strokeDasharray="3 3" className="stroke-gray-200 dark:stroke-gray-800" />}
          <XAxis {...xAxis} hide={compact} />
          <YAxis {...yAxis} hide={compact} />
          {!compact && <Tooltip {...tooltip} />}
          {series.map((s, i) => (
            <Line
              key={s}
              isAnimationActive={animate}
              type="monotone"
              dataKey={s}
              stroke={COLORS[i % COLORS.length]}
              strokeOpacity={opacityFor(view, s)}
              hide={view.hidden.has(s)}
              dot={false}
              strokeWidth={2}
            />
          ))}
          {!compact && view.onHover && <PointerSeries series={shownSeries} stacked={false} onHover={view.onHover} />}
        </LineChart>
      )
    case 'area':
      return (
        <AreaChart {...common}>
          {!compact && <CartesianGrid strokeDasharray="3 3" className="stroke-gray-200 dark:stroke-gray-800" />}
          <XAxis {...xAxis} hide={compact} />
          <YAxis {...yAxis} hide={compact} />
          {!compact && <Tooltip {...tooltip} />}
          {series.map((s, i) => (
            <Area
              key={s}
              isAnimationActive={animate}
              stackId={stackId}
              type="monotone"
              dataKey={s}
              stroke={COLORS[i % COLORS.length]}
              strokeOpacity={opacityFor(view, s)}
              fill={COLORS[i % COLORS.length]}
              fillOpacity={0.15 * opacityFor(view, s)}
              hide={view.hidden.has(s)}
              strokeWidth={2}
            />
          ))}
          {!compact && view.onHover && <PointerSeries series={shownSeries} stacked={stacked} onHover={view.onHover} />}
        </AreaChart>
      )
    case 'pie': {
      // Hidden slices leave the pie, so the rest fill it; each keeps the colour
      // of its place in the full list, which the legend shows.
      const shown = model.slices
        .map((row, i) => ({ row, color: COLORS[i % COLORS.length] }))
        .filter(({ row }) => !view.hidden.has(String(row[spec.x_key])))
      return (
        <PieChart margin={compact ? common.margin : { top: 4, right: 8, bottom: 0, left: 8 }}>
          {!compact && <Tooltip {...tooltip} />}
          <Pie
            data={shown.map(s => s.row)}
            dataKey={spec.series[0]}
            nameKey={spec.x_key}
            isAnimationActive={animate}
            // Fixed radii suit the full card; a preview scales to its box.
            innerRadius={compact ? '45%' : 40}
            outerRadius={compact ? '90%' : 75}
            paddingAngle={2}
            onMouseEnter={(_, i) => view.onHover?.(String(shown[i]?.row[spec.x_key] ?? ''))}
            onMouseLeave={() => view.onHover?.(null)}
          >
            {shown.map(({ row, color }) => (
              <Cell key={String(row[spec.x_key])} fill={color} fillOpacity={opacityFor(view, String(row[spec.x_key]))} />
            ))}
          </Pie>
        </PieChart>
      )
    }
    case 'bar':
    default:
      return (
        <BarChart {...common}>
          {!compact && <CartesianGrid strokeDasharray="3 3" className="stroke-gray-200 dark:stroke-gray-800" />}
          <XAxis {...xAxis} hide={compact} />
          <YAxis {...yAxis} hide={compact} />
          {!compact && <Tooltip {...tooltip} />}
          {series.map((s, i) => (
            // Rounding every segment of a stack makes the column look broken;
            // only the single-series case gets a cap.
            <Bar
              key={s}
              isAnimationActive={animate}
              stackId={stackId}
              dataKey={s}
              fill={COLORS[i % COLORS.length]}
              fillOpacity={opacityFor(view, s)}
              hide={view.hidden.has(s)}
              radius={stacked ? undefined : [3, 3, 0, 0]}
              {...hover(s)}
            />
          ))}
        </BarChart>
      )
  }
}
