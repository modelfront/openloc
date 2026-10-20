// Which dashboards a chart may go to. The menu mirrors the server's gates
// (mayWrite; move = both ends, copy = destination only), so a target it offers
// never 403s and a target the user may use is never hidden.
import { describe, it, expect } from 'vitest'
import { chartTargets } from './MoveCopyChartMenu'
import type { DashboardSummary } from '@/api'

const d = (id: number, personal: boolean): DashboardSummary =>
  ({ id, name: `D${id}`, personal, chart_count: 1, created_at: '', preview_charts: [] })

const SHARED_A = d(1, false)
const SHARED_B = d(2, false)
const MINE = d(3, true)
const ALL = [SHARED_A, SHARED_B, MINE]
const ids = (list: DashboardSummary[]) => list.map(x => x.id)

describe('chartTargets', () => {
  it('lets an admin move anywhere but where the chart already is', () => {
    const t = chartTargets(ALL, SHARED_A.id, true, true)
    expect(ids(t.move)).toEqual([2, 3])
  })

  // Copying beside the original is how you make a variant of it.
  it('offers the current dashboard as a copy target', () => {
    const t = chartTargets(ALL, SHARED_A.id, true, true)
    expect(ids(t.copy)).toEqual([1, 2, 3])
  })

  // A manager cannot write shared dashboards: they may not move a chart off
  // one, nor copy onto one — but copying it into their own is the point.
  it('lets a manager on a shared dashboard copy into their own, and nothing else', () => {
    const t = chartTargets(ALL, SHARED_A.id, false, false)
    expect(t.move).toEqual([])
    expect(ids(t.copy)).toEqual([3])
  })

  it('lets a manager move between dashboards they own', () => {
    const other = d(4, true)
    const t = chartTargets([...ALL, other], MINE.id, false, true)
    expect(ids(t.move)).toEqual([4])
  })

  it('offers nothing to a manager with no dashboard of their own', () => {
    const t = chartTargets([SHARED_A, SHARED_B], SHARED_A.id, false, false)
    expect(t).toEqual({ move: [], copy: [] })
  })
})
