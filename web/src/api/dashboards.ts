// Dashboards API client. Dashboards are shared or
// personal. A chart's data endpoint re-runs its stored definition through the
// metrics semantic layer on every call.
import { request } from './request'
import type { ChartSpec } from './chat'

export interface DashboardSummary {
  id: number
  name: string
  chart_count: number
  /** Visible only to the signed-in user; shared dashboards are org-wide. */
  personal: boolean
  created_at: string
  /** The dashboard's first charts, in its order, for the card's preview. */
  preview_charts: DashboardPreviewChart[]
}

/** What create and duplicate answer with: the new row, before it has charts to preview. */
export type NewDashboard = Omit<DashboardSummary, 'preview_charts'>

export interface DashboardPreviewChart {
  id: number
  title: string
  viz: DashboardChart['viz']
}

export interface ChartDefinition {
  measure: string
  dimensions?: string[]
  filters?: Record<string, string>
  last_days: number
}

export interface DashboardChart {
  id: number
  title: string
  definition: ChartDefinition
  viz: Pick<ChartSpec, 'type' | 'x_key' | 'series' | 'series_key'>
  /** Controls this chart shows IN ADDITION to the dashboard's. Selections live
   *  in the URL namespaced by chart id, and win over the dashboard's. */
  controls: DashboardFilterConfig
  /** Controls this chart COULD offer, computed from its measure. */
  available_controls: string[]
  position: number
}

/** Which dashboard-level controls this dashboard shows. Values live in the URL. */
export interface DashboardFilterConfig {
  controls: string[]
}

export interface DashboardDetail {
  id: number
  name: string
  personal: boolean
  created_at: string
  filters: DashboardFilterConfig
  /** Controls the dashboard COULD offer — the union across its charts. */
  available_controls: string[]
  charts: DashboardChart[]
}

export interface ChartData {
  measure: string
  definition: string
  columns: string[]
  rows: Record<string, unknown>[]
  /** Rows were DROPPED: any total or "top N" read off this chart is partial. */
  truncated?: boolean
  /** The smallest values were combined into one "Other" row; nothing was lost. */
  folded?: boolean
  /** The time bucket the answer actually uses — wider than requested when the
   *  range could not carry the finer one. */
  grain?: string
  /** Value columns that are averages, not parts of a total. */
  averages?: string[]
  /** What each value column counts ("words", "minutes"). */
  units?: Record<string, string>
  /** Value columns that are sums of other value columns. */
  derived?: string[]
  /** Effective x axis — the granularity control renames the time column. */
  x_key?: string
}

/**
 * Dashboard filter selections. `unit` never reaches the server: every volume
 * measure returns words, segments and characters in the same row, so the card
 * just plots a different column.
 */
export interface DashboardFilterValues {
  last_days?: number
  grain?: string
  unit?: string
  target_language?: string
  source_language?: string
  customer?: string
  project?: string
  user?: string
  ai_engine?: string
}

export const listDashboards = (): Promise<DashboardSummary[]> =>
  request<{ data: DashboardSummary[] }>('/dashboards').then(r => r.data)

export const getDashboard = (id: number): Promise<DashboardDetail> =>
  request<{ data: DashboardDetail }>(`/dashboards/${id}`).then(r => r.data)

/** Serializes the selections the SERVER acts on; `unit` stays client-side. */
export function chartDataQuery(values: DashboardFilterValues = {}): string {
  const p = new URLSearchParams()
  for (const [key, val] of Object.entries(values)) {
    if (key === 'unit' || val === undefined || val === '') continue
    p.set(key, String(val))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

export const getChartData = (chartId: number, values?: DashboardFilterValues): Promise<ChartData> =>
  request<{ data: ChartData }>(
    `/dashboards/charts/${chartId}/data${chartDataQuery(values)}`,
  ).then(r => r.data)

export const updateDashboardFilters = (id: number, config: DashboardFilterConfig): Promise<void> =>
  request(`/dashboards/${id}/filters`, { method: 'PUT', body: JSON.stringify(config) })

/** Dimension name → the values that have data in the window. */
export type DashboardFilterOptions = Record<string, string[]>

/**
 * Options for the dimension dropdowns, derived from the data in the selected
 * window — so narrowing the date range narrows the lists with it.
 */
export const getFilterOptions = (id: number, lastDays: number): Promise<DashboardFilterOptions> =>
  request<{ data: DashboardFilterOptions }>(
    `/dashboards/${id}/filter-options?last_days=${lastDays}`,
  ).then(r => r.data)

export const deleteChart = (chartId: number): Promise<void> =>
  request(`/dashboards/charts/${chartId}`, { method: 'DELETE' })

export const deleteDashboard = (id: number): Promise<void> =>
  request(`/dashboards/${id}`, { method: 'DELETE' })

/** Sets the order of ONE tab's dashboards; send every id in that tab, in order. */
export const reorderDashboards = (dashboardIds: number[]): Promise<void> =>
  request('/dashboards/order', { method: 'PUT', body: JSON.stringify({ dashboard_ids: dashboardIds }) })

/** Sets the card order on a dashboard; send every chart id, in order. */
export const reorderCharts = (dashboardId: number, chartIds: number[]): Promise<void> =>
  request(`/dashboards/${dashboardId}/chart-order`, { method: 'PUT', body: JSON.stringify({ chart_ids: chartIds }) })

/**
 * Renames a dashboard. 409 when the name is taken on this dashboard's side —
 * names are unique per tenant among shared dashboards, and per owner among
 * personal ones, because the chat resolves dashboards BY name.
 */
export const renameDashboard = (id: number, name: string): Promise<void> =>
  request(`/dashboards/${id}/name`, { method: 'PUT', body: JSON.stringify({ name }) })

/** Retitles a chart. Titles are not unique, so there is no conflict case. */
export const renameChart = (chartId: number, name: string): Promise<void> =>
  request(`/dashboards/charts/${chartId}/title`, { method: 'PUT', body: JSON.stringify({ name }) })

/**
 * A chart reached by SHARE TOKEN. Carries the dashboard's name for context and
 * its filter controls so the view is the same one the sender saw — but nothing
 * else about a dashboard the reader may not be able to browse.
 */
export interface SharedChart {
  title: string
  definition: ChartDefinition
  viz: Pick<ChartSpec, 'type' | 'x_key' | 'series' | 'series_key'>
  dashboard: string
  filters: DashboardFilterConfig
  /** Controls that change this chart — what a download lists as its filters. */
  available_controls: string[]
}

/** Issues a share link, replacing any previous one for this chart. */
export const shareChart = (chartId: number): Promise<string> =>
  request<{ data: { token: string } }>(`/dashboards/charts/${chartId}/share`, { method: 'POST' })
    .then(r => r.data.token)

/** Kills the link. Idempotent — revoking an unshared chart is a success. */
export const revokeChartShare = (chartId: number): Promise<void> =>
  request(`/dashboards/charts/${chartId}/share`, { method: 'DELETE' })

export const getSharedChart = (token: string): Promise<SharedChart> =>
  request<{ data: SharedChart }>(`/shared/charts/${encodeURIComponent(token)}`).then(r => r.data)

export const getSharedChartData = (token: string, values?: DashboardFilterValues): Promise<ChartData> =>
  request<{ data: ChartData }>(
    `/shared/charts/${encodeURIComponent(token)}/data${chartDataQuery(values)}`,
  ).then(r => r.data)

/**
 * Copies a dashboard and its charts to the given side, named "Copy of X".
 * The copy carries the filter controls but NO share links — nobody has shared
 * anything from a dashboard that did not exist a moment ago.
 */
export const duplicateDashboard = (id: number, personal: boolean): Promise<NewDashboard> =>
  request<{ data: NewDashboard }>(`/dashboards/${id}/duplicate`, {
    method: 'POST', body: JSON.stringify({ personal }),
  }).then(r => r.data)

/** What a reset would replace, so the confirmation can name real numbers. */
export interface SharedDashboardCount {
  shared: number
  defaults: number
  /** People who would lose pinned charts if the shared dashboards went. */
  pinned_users: number
}

export const getSharedDashboardCount = (): Promise<SharedDashboardCount> =>
  request<{ data: SharedDashboardCount }>('/dashboards/shared-count').then(r => r.data)

/**
 * Replaces every SHARED dashboard with the built-in set. Destructive: shared
 * dashboards the team built are deleted, and the share links of their charts
 * die with them. Personal dashboards are untouched.
 */
export const resetSharedDashboards = (): Promise<void> =>
  request('/dashboards/reset-defaults', { method: 'POST' })

/** Sets a chart's own control list. Dashboard-wide controls are refused here. */
export const updateChartControls = (chartId: number, config: DashboardFilterConfig): Promise<void> =>
  request(`/dashboards/charts/${chartId}/filters`, { method: 'PUT', body: JSON.stringify(config) })

/**
 * Removes one control, freezing its CURRENT value into the charts it was
 * reaching so nothing they show changes. The value comes from here because
 * selections live in the URL — which means the remover freezes the selection
 * THEY are looking at, for everyone.
 */
export const removeDashboardFilter = (id: number, control: string, value: string): Promise<void> =>
  request(`/dashboards/${id}/filters/remove`, { method: 'POST', body: JSON.stringify({ control, value }) })

/** Same, for one chart. Falls back to the dashboard's control when it has one. */
export const removeChartFilter = (chartId: number, control: string, value: string): Promise<void> =>
  request(`/dashboards/charts/${chartId}/filters/remove`, { method: 'POST', body: JSON.stringify({ control, value }) })

/**
 * Moves a chart to another dashboard. Needs write access on BOTH ends: it
 * leaves one dashboard and joins another. A 409 with code `filter_override`
 * means the destination's controls would take over a filter the chart fixes
 * itself; resend with `confirmOverride` once the user agrees.
 */
export const moveChart = (chartId: number, dashboardId: number, confirmOverride = false): Promise<void> =>
  request(`/dashboards/charts/${chartId}/dashboard`, {
    method: 'PUT', body: JSON.stringify({ dashboard_id: dashboardId, confirm_override: confirmOverride }),
  })

/**
 * Copies a chart onto a dashboard — gated on the DESTINATION only, so anyone
 * who can see a chart may copy it into their own dashboard. The copy keeps its
 * title elsewhere and becomes "Copy of …" beside its original. Same 409 as a
 * move when the destination's controls would override the chart's own filter.
 */
export const copyChart = (chartId: number, dashboardId: number, confirmOverride = false): Promise<{ id: number; title: string }> =>
  request<{ data: { id: number; title: string } }>(`/dashboards/charts/${chartId}/copy`, {
    method: 'POST', body: JSON.stringify({ dashboard_id: dashboardId, confirm_override: confirmOverride }),
  }).then(r => r.data)

/** Moves a dashboard between Shared and My dashboards. */
export const setDashboardVisibility = (id: number, personal: boolean): Promise<void> =>
  request(`/dashboards/${id}/visibility`, { method: 'PUT', body: JSON.stringify({ personal }) })

/** One output column of a measure. `unit` is what the number counts: two
 *  columns belong on the same axis only when their units match. */
export interface CatalogValue {
  name: string
  unit: string
  /** A mean — never gap-filled, never folded into "Other". */
  average?: boolean
}

/** One measure as the chart builder sees it: what it means, how it can be
 *  grouped, which numbers it returns, and the filters it can carry.
 *  Generated from the Go registry. */
export interface CatalogMeasure {
  name: string
  definition: string
  /** Categorical groupings only — time grains travel in `grains`. */
  dimensions: string[]
  values: CatalogValue[]
  /** The widest control set this measure can carry; `granularity` applies only
   *  when the axis is a time grain. */
  filters: string[]
}

export interface MetricsCatalog {
  measures: CatalogMeasure[]
  grains: string[]
}

export const getMetricsCatalog = (): Promise<MetricsCatalog> =>
  request<{ data: MetricsCatalog }>('/dashboards/metrics-catalog').then(r => r.data)

/** Creates an EMPTY dashboard — a folder to put charts in. */
export const createDashboard = (name: string, personal: boolean): Promise<NewDashboard> =>
  request<{ data: NewDashboard }>('/dashboards', {
    method: 'POST', body: JSON.stringify({ name, personal }),
  }).then(r => r.data)

/**
 * A chart built in the dialog. No `last_days`: the date-range control owns the
 * window, and the server stores the default.
 */
export interface NewChart {
  title: string
  measure: string
  dimensions: string[]
  chart_type: string
  x_key: string
  series: string[]
  series_key?: string
  /** Controls this chart shows on top of the dashboard's. */
  controls?: string[]
}

export const createChart = (dashboardId: number, chart: NewChart): Promise<{ id: number }> =>
  request<{ data: { id: number } }>(`/dashboards/${dashboardId}/charts`, {
    method: 'POST', body: JSON.stringify(chart),
  }).then(r => r.data)

/**
 * Re-states an existing chart from the builder — title, definition, drawing and
 * filters at once. Editing replaces the whole chart rather than patching it: a
 * measure change invalidates every other field.
 */
export const updateChart = (chartId: number, chart: NewChart): Promise<void> =>
  request(`/dashboards/charts/${chartId}`, { method: 'PUT', body: JSON.stringify(chart) })

export interface ChartSuggestion {
  source: 'ai' | 'none'
  prompt?: string
  /** Short human line for the card face (prompt minus boilerplate). */
  label?: string
  hint?: string
}

/**
 * A chart prompt written for this dashboard: AI, else a greyed hint.
 * `shared` asks for a suggestion that lands org-wide — the server ignores it
 * for anyone who couldn't create a shared dashboard anyway.
 */
export const suggestChart = (dashboardId?: number, shared?: boolean): Promise<ChartSuggestion> => {
  const p = new URLSearchParams()
  if (dashboardId) p.set('dashboard_id', String(dashboardId))
  if (shared) p.set('shared', '1')
  const q = p.toString()
  return request<{ data: ChartSuggestion }>(`/chat/suggest-chart${q ? `?${q}` : ''}`).then(r => r.data)
}
