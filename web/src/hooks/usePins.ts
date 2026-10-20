import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getPinState, pinChart, unpinChart, pinDashboard, unpinDashboard } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from './use-mutation-with-toast'

type Toggle = { id: number; pin: boolean }

/**
 * What the signed-in user has pinned, and the toggles for it. Spread a
 * `chart(id)` or `dashboard(id)` result into a PinButton.
 */
export function usePins() {
  const { data } = useQuery({ queryKey: queryKeys.dashboards.pins, queryFn: getPinState, staleTime: 30_000 })
  const charts = useMemo(() => new Set(data?.chart_ids ?? []), [data])
  const dashboards = useMemo(() => new Set(data?.dashboard_ids ?? []), [data])

  const chartMut = useMutationWithToast({
    mutationFn: ({ id, pin }: Toggle) => (pin ? pinChart(id) : unpinChart(id)).then(() => pin),
    successMessage: pin => (pin ? 'Chart pinned' : 'Chart unpinned'),
    errorMessage: 'Failed to update the pin',
    invalidateKeys: [queryKeys.dashboards.all],
  })
  const dashboardMut = useMutationWithToast({
    mutationFn: ({ id, pin }: Toggle) => (pin ? pinDashboard(id) : unpinDashboard(id)).then(() => pin),
    successMessage: pin => (pin ? 'Dashboard pinned' : 'Dashboard unpinned'),
    errorMessage: 'Failed to update the pin',
    invalidateKeys: [queryKeys.dashboards.all],
  })

  return {
    chart: (id: number) => {
      const pinned = charts.has(id)
      return { pinned, loading: chartMut.isPending, onToggle: () => chartMut.mutate({ id, pin: !pinned }) }
    },
    dashboard: (id: number) => {
      const pinned = dashboards.has(id)
      return { pinned, loading: dashboardMut.isPending, onToggle: () => dashboardMut.mutate({ id, pin: !pinned }) }
    },
  }
}
