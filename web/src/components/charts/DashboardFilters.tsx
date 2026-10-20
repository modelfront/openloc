// Filter state for the dashboard page and the single-chart page.
//
// Selections live in the URL, never in component state or storage: that is what
// makes a filtered view a shareable link. Both pages read them the same way, so
// clicking from a dashboard into one chart — and copying the link from there —
// carries the selection with no translation step.
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getFilterOptions, getPinnedFilterOptions, type DashboardFilterValues, type DashboardFilterOptions } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { DASHBOARDS_PATH, MY_DASHBOARDS_PATH, ORG_DASHBOARDS_PATH } from './dashboard-paths'
import { DashboardFilterBar, DIMENSION_LABELS, FILTER_DEFAULTS } from './DashboardFilterBar'

/** Controls that never narrow a query — they reshape what the card plots. */
const SPECIAL_CONTROLS = ['date_range', 'unit', 'granularity']

/**
 * Serializes a selection into query parameters.
 *
 * Unlike `chartDataQuery` this keeps `unit`, because the URL describes what the
 * READER sees and the unit is a column swap the client makes. Dropping it would
 * hand someone a link to a chart counting words when you were looking at
 * characters.
 */
export function toSearchParams(values: DashboardFilterValues, base?: URLSearchParams): URLSearchParams {
  const p = new URLSearchParams(base)
  for (const [key, val] of Object.entries(values)) {
    if (val === undefined || val === '') p.delete(key)
    else p.set(key, String(val))
  }
  return p
}

/**
 * The current selection, with every absent parameter resolved to the dashboard
 * default rather than to "leave the chart as it was" — so a fresh load and a
 * shared link agree, and every card answers the same question over the same
 * window.
 */
export function useFilterValues(): [DashboardFilterValues, (next: DashboardFilterValues) => void] {
  const [params, setParams] = useSearchParams()
  const lastDays = Number(params.get('last_days'))
  const values: DashboardFilterValues = {
    last_days: Number.isFinite(lastDays) && lastDays > 0 ? lastDays : FILTER_DEFAULTS.last_days,
    grain: params.get('grain') ?? FILTER_DEFAULTS.grain,
    unit: params.get('unit') ?? FILTER_DEFAULTS.unit,
    // Every dimension the bar can offer, read from the same list the bar
    // renders from: one missing here is a dropdown that snaps back to "All".
    ...Object.fromEntries(Object.keys(DIMENSION_LABELS).map(k => [k, params.get(k) ?? undefined])),
  }
  const setValues = (next: DashboardFilterValues) => setParams(toSearchParams(next, params), { replace: true })
  return [values, setValues]
}

/**
 * The filter bar wired to its option lists. Dimension dropdowns list only what
 * has data in the selected window, so the query is keyed by the range and
 * refetches when it changes; a dashboard with no dimension control skips it.
 */
export function ConnectedFilterBar({ controls, values, options, onChange }: {
  controls: string[]
  values: DashboardFilterValues
  options?: DashboardFilterOptions
  onChange: (next: DashboardFilterValues) => void
}) {
  return <DashboardFilterBar controls={controls} values={values} options={options} onChange={onChange} />
}

/**
 * The dimension values that have data in the window, for the whole page.
 *
 * One fetch shared by the dashboard bar and every per-chart bar: the endpoint
 * already returns the union of the dashboard's controls and each chart's own,
 * so a second request per card would ask the same question again.
 */
export function useFilterOptions(dashboardId: number, lastDays: number, anyDimensionControl: boolean, pinned = false) {
  const { data } = useQuery({
    queryKey: pinned
      ? queryKeys.dashboards.pinnedFilterOptions(dashboardId, lastDays)
      : queryKeys.dashboards.filterOptions(dashboardId, lastDays),
    queryFn: () => (pinned ? getPinnedFilterOptions : getFilterOptions)(dashboardId, lastDays),
    enabled: anyDimensionControl,
    staleTime: 5 * 60_000,
  })
  return data
}

/** True when a control needs a data-derived option list. */
export const needsOptions = (controls: string[]) => controls.some(c => !SPECIAL_CONTROLS.includes(c))

// The trail names the SPACE as well as the page: "Organization dashboards" and
// "My dashboards" are different audiences, and which one you are editing
// decides whether a change is visible to the org. Losing that distinction in a
// breadcrumb is how someone renames the org's dashboard thinking it is their scratch copy.
export function dashboardCrumbs(personal: boolean) {
  return [
    { label: 'Dashboards', to: DASHBOARDS_PATH },
    personal
      ? { label: 'My dashboards', to: MY_DASHBOARDS_PATH }
      : { label: 'Organization dashboards', to: ORG_DASHBOARDS_PATH },
  ]
}

// Per-chart selections travel in the SAME url as the dashboard's, namespaced by
// chart id: `?last_days=30&c11.customer=Acme`. That keeps the one property the
// whole design rests on — a narrowed view is a link you can send — while the
// request each card already makes stays flat, because it only ever carries its
// own chart's filters.
const chartParam = (chartId: number, key: string) => `c${chartId}.${key}`

/**
 * Control name → the field it sets. They differ for the three that RESHAPE a
 * card rather than narrow it: a control called "granularity" sets `grain`, and
 * "date_range" sets `last_days`. Dimension controls are named after their own
 * field, which is why this only has three entries.
 */
export const CONTROL_FIELD: Record<string, keyof DashboardFilterValues> = {
  date_range: 'last_days',
  unit: 'unit',
  granularity: 'grain',
}

const fieldFor = (control: string) => CONTROL_FIELD[control] ?? (control as keyof DashboardFilterValues)

/**
 * One chart's own selections, and a setter that writes them back to the URL.
 *
 * Returned MERGED over the dashboard's: the chart's own pick wins, because it
 * is the more specific of the two. A dashboard can gain a control later, so the
 * rule has to hold here at render time rather than only when the chart was
 * configured.
 */
export function useChartFilters(
  chartId: number,
  controls: string[],
  dashboardValues: DashboardFilterValues,
): [DashboardFilterValues, DashboardFilterValues, (next: DashboardFilterValues) => void] {
  const [params, setParams] = useSearchParams()

  const own: DashboardFilterValues = {}
  for (const control of controls) {
    const field = fieldFor(control)
    const v = params.get(chartParam(chartId, field))
    // last_days is the one numeric field; the rest are strings.
    if (v) own[field] = (field === 'last_days' ? Number(v) : v) as never
  }
  const setOwn = (next: DashboardFilterValues) => {
    const p = new URLSearchParams(params)
    for (const control of controls) {
      const field = fieldFor(control)
      const v = next[field]
      if (v === undefined || v === '') p.delete(chartParam(chartId, field))
      else p.set(chartParam(chartId, field), String(v))
    }
    setParams(p, { replace: true })
  }
  return [{ ...dashboardValues, ...own }, own, setOwn]
}
