// Dashboard-level filters: one control row that narrows every chart on the
// dashboard.
//
// Which controls appear is stored per dashboard; the SELECTED values live in
// the URL, so a filtered view is a shareable link and nothing is saved per
// user. Every control is the app's one searchable select (`Combobox`) inside a
// `FormField`, so the row reads as one labelled thing rather than a line of
// anonymous widgets.
import { Combobox, type ComboboxOption } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import type { ChartData, ChartSpec, DashboardChart, DashboardFilterOptions, DashboardFilterValues } from '@/api'
import { metricLabel } from '@/lib/metric-labels'

/** Relative windows, spanning days to months. The server caps last_days at 365. */
export const DATE_RANGES: ComboboxOption[] = [
  { value: '7', label: 'Last 7 days' },
  { value: '30', label: 'Last 30 days' },
  { value: '90', label: 'Last 3 months' },
  { value: '180', label: 'Last 6 months' },
  { value: '365', label: 'Last 12 months' },
]

/** Value columns every volume measure returns in the same row. */
export const UNITS: ComboboxOption[] = [
  { value: 'words', label: 'Words' },
  { value: 'segments', label: 'Segments' },
  { value: 'characters', label: 'Characters' },
]

export const GRAINS: ComboboxOption[] = [
  { value: 'day', label: 'Daily' },
  { value: 'week', label: 'Weekly' },
  { value: 'month', label: 'Monthly' },
]

// Every control's human name, in one place: the bar labels its fields from it
// and the "add filter" menu names its items from it, so the two cannot drift.
export const CONTROL_LABELS: Record<string, string> = {
  date_range: 'Date range',
  unit: 'Unit',
  granularity: 'Granularity',
  target_language: 'Target language',
  source_language: 'Source language',
  customer: 'Customer',
  project: 'Project',
  user: 'User',
  ai_engine: 'AI engine',
}

// One width for every control, so the row reads as evenly spaced.
//
// The width goes on the FIELD as well as the wrapper: a Combobox trigger is a
// plain flex box with no width of its own, so it shrinks to its content. Long
// values truncate rather than widening the field.
const CONTROL_WIDTH = 'w-44'
const FIELD_WIDTH = 'w-full'

/**
 * What the page shows before anyone touches a control. Real values, not "leave
 * each chart as it was": the dashboard decides the view, so every card answers
 * the same question over the same window.
 */
export const FILTER_DEFAULTS = { last_days: 90, unit: 'words', grain: 'week' } as const

// Dimension controls: the field label plus its "no filter" row. Unlike the
// three above, "everything" IS a real answer here, so that row stays.
export const DIMENSION_LABELS: Record<string, { label: string; allLabel: string }> = {
  target_language: { label: 'Target language', allLabel: 'All target languages' },
  source_language: { label: 'Source language', allLabel: 'All source languages' },
  customer: { label: 'Customer', allLabel: 'All customers' },
  project: { label: 'Project', allLabel: 'All projects' },
  user: { label: 'User', allLabel: 'All users' },
  ai_engine: { label: 'AI engine', allLabel: 'All AI engines' },
}

/**
 * Which value column a card plots under the unit control. The control is a
 * column swap, not a query change, so it applies only where the measure
 * actually returns that column — "QA issues" has no words, and must keep
 * plotting issues rather than render an empty axis.
 */
export function seriesForUnit(unit: string | undefined, columns: string[], fallback: string[]): string[] {
  return unit && columns.includes(unit) ? [unit] : fallback
}

/**
 * What a card draws: the stored drawing, applied to the rows this selection
 * returned. The one place that decision is made — the chart and its
 * spreadsheet export both read it, so a download is always the chart on screen
 * rather than a second opinion about it.
 */
export function chartSpecFor(viz: DashboardChart['viz'], data: ChartData, unit: string | undefined): ChartSpec {
  return {
    ...viz,
    // The granularity control renames the time column, so the server reports
    // the axis it actually produced.
    x_key: data.x_key || viz.x_key,
    series: seriesForUnit(unit, data.columns, viz.series),
    averages: data.averages,
    units: data.units,
    derived: data.derived,
    data: data.rows,
  }
}

interface Props {
  /** Control names from the dashboard's stored config. */
  controls: string[]
  values: DashboardFilterValues
  /** Dimension values with data in the window; absent while loading. */
  options?: DashboardFilterOptions
  onChange: (next: DashboardFilterValues) => void
}

// Which value a control is currently set to, as the string the freeze stores.
export function controlValue(control: string, values: DashboardFilterValues): string {
  switch (control) {
    case 'date_range': return String(values.last_days ?? '')
    case 'unit': return values.unit ?? ''
    case 'granularity': return values.grain ?? ''
    default: return String(values[control as keyof DashboardFilterValues] ?? '')
  }
}

/**
 * The selection a chart was drawn under, in the filter bar's own words — so a
 * downloaded table says what it was narrowed to. Only controls the chart
 * responds to: a dashboard filter its measure cannot take was ignored, and
 * listing it would claim the numbers were narrowed when they were not.
 * `fixed` are values frozen into the chart itself, which apply regardless.
 */
export function describeFilters(
  values: DashboardFilterValues,
  applicable: string[],
  fixed: Record<string, string> = {},
): [string, string][] {
  const named = (options: ComboboxOption[], raw: string) => options.find(o => o.value === raw)?.label ?? raw
  const out: [string, string][] = []
  for (const control of applicable) {
    const raw = controlValue(control, values)
    if (!raw) continue
    const shown =
      control === 'date_range' ? named(DATE_RANGES, raw).replace(/^\d+$/, n => `Last ${n} days`)
      : control === 'unit' ? named(UNITS, raw)
      : control === 'granularity' ? named(GRAINS, raw)
      : raw
    out.push([CONTROL_LABELS[control] ?? metricLabel(control), shown])
  }
  for (const [dimension, value] of Object.entries(fixed)) {
    out.push([CONTROL_LABELS[dimension] ?? metricLabel(dimension), `${value} (fixed on this chart)`])
  }
  return out
}

export function DashboardFilterBar({ controls, values, options, onChange }: Props) {
  // No controls, no bar — the edit menu is where the first one is added.
  if (controls.length === 0) return null

  const has = (name: string) => controls.includes(name)
  const set = (patch: DashboardFilterValues) => onChange({ ...values, ...patch })

  return (
    <div className="mb-4 flex flex-wrap items-end gap-3" data-testid="dashboard-filter-bar">
      {has('date_range') && (
        <FormField label="Date range" className={CONTROL_WIDTH}>
          <Combobox
            data-testid="dashboard-filter-date-range"
            aria-label="Date range"
            className={FIELD_WIDTH}
            options={DATE_RANGES}
            value={String(values.last_days ?? FILTER_DEFAULTS.last_days)}
            onChange={v => set({ last_days: Number(v) })}
          />
        </FormField>
      )}
      {has('unit') && (
        <FormField label="Unit" className={CONTROL_WIDTH}>
          <Combobox
            data-testid="dashboard-filter-unit"
            aria-label="Unit"
            className={FIELD_WIDTH}
            options={UNITS}
            value={values.unit ?? FILTER_DEFAULTS.unit}
            onChange={v => set({ unit: v })}
          />
        </FormField>
      )}
      {has('granularity') && (
        <FormField label="Granularity" className={CONTROL_WIDTH}>
          <Combobox
            data-testid="dashboard-filter-granularity"
            aria-label="Granularity"
            className={FIELD_WIDTH}
            options={GRAINS}
            value={values.grain ?? FILTER_DEFAULTS.grain}
            onChange={v => set({ grain: v })}
          />
        </FormField>
      )}
      {Object.entries(DIMENSION_LABELS).map(([key, meta]) =>
        has(key) ? (
          <FormField key={key} label={meta.label} className={CONTROL_WIDTH}>
            <Combobox
              data-testid={`dashboard-filter-${key.replace(/_/g, '-')}`}
              aria-label={meta.label}
              className={FIELD_WIDTH}
              options={[
                { value: '', label: meta.allLabel },
                ...(options?.[key] ?? []).map(v => ({ value: v, label: v })),
              ]}
              value={String(values[key as keyof DashboardFilterValues] ?? '')}
              onChange={v => set({ [key]: v || undefined })}
            />
          </FormField>
        ) : null,
      )}
    </div>
  )
}
