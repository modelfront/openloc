// The Pinned view: a person's own dashboards of references to charts that live
// on other dashboards. Pinning never copies a chart, so its data, title and
// definition stay the original's.
import { request } from './request'
import type { DashboardChart, DashboardFilterConfig, DashboardFilterOptions, DashboardPreviewChart } from './dashboards'

/** What the signed-in user has pinned, for the pin icons. */
export interface PinState {
  chart_ids: number[]
  /** Dashboards whose every chart is pinned, or that are pinned whole. */
  dashboard_ids: number[]
}

export interface PinnedDashboardSummary {
  id: number
  name: string
  chart_count: number
  preview_charts: DashboardPreviewChart[]
}

/** A pinned chart, with the dashboard it lives on. */
export interface PinnedChart extends DashboardChart {
  dashboard_id: number
  dashboard_name: string
  dashboard_personal: boolean
}

export interface PinnedDashboardDetail {
  id: number
  name: string
  created_at: string
  filters: DashboardFilterConfig
  available_controls: string[]
  charts: PinnedChart[]
}

export const getPinState = (): Promise<PinState> =>
  request<{ data: PinState }>('/dashboards/pins').then(r => r.data)

export const listPinnedDashboards = (): Promise<PinnedDashboardSummary[]> =>
  request<{ data: PinnedDashboardSummary[] }>('/dashboards/pinned').then(r => r.data)

export const getPinnedDashboard = (id: number): Promise<PinnedDashboardDetail> =>
  request<{ data: PinnedDashboardDetail }>(`/dashboards/pinned/${id}`).then(r => r.data)

export const pinChart = (chartId: number): Promise<void> =>
  request(`/dashboards/charts/${chartId}/pin`, { method: 'POST' })

export const unpinChart = (chartId: number): Promise<void> =>
  request(`/dashboards/charts/${chartId}/pin`, { method: 'DELETE' })

export const pinDashboard = (dashboardId: number): Promise<void> =>
  request(`/dashboards/${dashboardId}/pin`, { method: 'POST' })

export const unpinDashboard = (dashboardId: number): Promise<void> =>
  request(`/dashboards/${dashboardId}/pin`, { method: 'DELETE' })

/** Removes a pinned dashboard and its references; the originals stay. */
export const deletePinnedDashboard = (id: number): Promise<void> =>
  request(`/dashboards/pinned/${id}`, { method: 'DELETE' })

export const renamePinnedDashboard = (id: number, name: string): Promise<void> =>
  request(`/dashboards/pinned/${id}/name`, { method: 'PUT', body: JSON.stringify({ name }) })

/** Replaces the pinned dashboard's control list. */
export const updatePinnedFilters = (id: number, config: DashboardFilterConfig): Promise<void> =>
  request(`/dashboards/pinned/${id}/filters`, { method: 'PUT', body: JSON.stringify(config) })

export const getPinnedFilterOptions = (id: number, lastDays: number): Promise<DashboardFilterOptions> =>
  request<{ data: DashboardFilterOptions }>(
    `/dashboards/pinned/${id}/filter-options?last_days=${lastDays}`,
  ).then(r => r.data)

/** Sets the order of the pinned dashboards; send every id, in order. */
export const reorderPinnedDashboards = (ids: number[]): Promise<void> =>
  request('/dashboards/pinned/order', { method: 'PUT', body: JSON.stringify({ pinned_ids: ids }) })

/** Sets the card order on a pinned dashboard; send every chart id, in order. */
export const reorderPinnedCharts = (id: number, chartIds: number[]): Promise<void> =>
  request(`/dashboards/pinned/${id}/chart-order`, { method: 'PUT', body: JSON.stringify({ chart_ids: chartIds }) })

/** Moves a pinned chart to another pinned dashboard. */
export const movePinnedChart = (chartId: number, pinnedId: number): Promise<void> =>
  request(`/dashboards/pinned/charts/${chartId}/dashboard`, {
    method: 'PUT',
    body: JSON.stringify({ pinned_dashboard_id: pinnedId }),
  })
