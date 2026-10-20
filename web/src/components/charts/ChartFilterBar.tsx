// A chart's OWN control row.
//
// Sits between the card title and the chart, always visible rather than behind
// an icon: a card narrowed differently from the ones beside it should look
// different from them, or the dashboard quietly compares unlike things.
//
// Any control the dashboard can show, a chart can show too — including date
// range, unit and granularity. A chart set to Monthly beside a dashboard set to
// Weekly displays its own dropdown saying Monthly, so the divergence is visible
// rather than a trap. That is what makes the inline row the right placement.
import { Combobox } from '@/components/ui/combobox'
import type { DashboardFilterOptions, DashboardFilterValues } from '@/api'
import { DIMENSION_LABELS, DATE_RANGES, UNITS, GRAINS, FILTER_DEFAULTS } from './DashboardFilterBar'

// The three controls whose options are fixed rather than derived from the data:
// which field they set, and what they fall back to when nothing is chosen.
const RESHAPING = {
  date_range: { key: 'last_days' as const, label: 'Date range', options: DATE_RANGES, fallback: String(FILTER_DEFAULTS.last_days), toValue: (v: string) => Number(v) },
  unit: { key: 'unit' as const, label: 'Unit', options: UNITS, fallback: FILTER_DEFAULTS.unit, toValue: (v: string) => v },
  granularity: { key: 'grain' as const, label: 'Granularity', options: GRAINS, fallback: FILTER_DEFAULTS.grain, toValue: (v: string) => v },
}

export function ChartFilterBar({ controls, values, inherited, options, onChange, reserveSpace }: {
  /**
   * Render an empty row of the same height when this chart has no controls.
   *
   * Cards sit in a CSS grid, so they already share a height — but a filter row
   * on one card pushes its chart down, and two charts starting at different
   * heights read as misaligned even though their boxes are not. Reserving the
   * row on every card in the grid lines the charts up, and is skipped entirely
   * when no card on the dashboard has its own filters.
   */
  reserveSpace?: boolean
  controls: string[]
  /** This chart's OWN selections — not the dashboard's, which it inherits. */
  values: DashboardFilterValues
  /** What the dashboard is set to, so an untouched control shows the truth. */
  inherited: DashboardFilterValues
  options?: DashboardFilterOptions
  onChange: (next: DashboardFilterValues) => void
}) {
  if (controls.length === 0) {
    // h-7 matches the compact Combobox height used below; mb-2 matches the row.
    return reserveSpace ? <div className="mb-2 h-7" aria-hidden="true" /> : null
  }
  return (
    <div className="mb-2 flex flex-wrap items-center gap-2" data-testid="chart-filter-bar">
      {controls.map(key => {
        const fixed = RESHAPING[key as keyof typeof RESHAPING]
        if (fixed) {
          // Falls back to the DASHBOARD's current value, not a constant: an
          // untouched chart control has to show what the card is actually
          // doing, or it reads as an override that is not in effect.
          const current = values[fixed.key] ?? inherited[fixed.key] ?? fixed.fallback
          return (
            <span key={key} className="flex items-center gap-1">
            <Combobox
              data-testid={`chart-filter-${key.replace(/_/g, '-')}`}
              aria-label={fixed.label}
              className="h-7 w-36 text-xs"
              options={fixed.options}
              value={String(current)}
              onChange={v => onChange({ ...values, [fixed.key]: fixed.toValue(v) })}
            />
            </span>
          )
        }
        const meta = DIMENSION_LABELS[key]
        if (!meta) return null
        return (
          <span key={key} className="flex items-center gap-1">
          <Combobox
            data-testid={`chart-filter-${key.replace(/_/g, '-')}`}
            aria-label={meta.label}
            className="h-7 w-40 text-xs"
            options={[
              { value: '', label: meta.allLabel },
              ...(options?.[key] ?? []).map(v => ({ value: v, label: v })),
            ]}
            value={String(values[key as keyof DashboardFilterValues] ?? '')}
            onChange={v => onChange({ ...values, [key]: v || undefined })}
          />
          </span>
        )
      })}
    </div>
  )
}
