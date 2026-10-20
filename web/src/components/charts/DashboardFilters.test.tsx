// The dashboard selection lives in the URL. Every dimension the filter bar can
// offer must survive the round trip, or picking it snaps back to "All …" and
// never reaches a chart request or a shared link.
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { useFilterValues } from './DashboardFilters'
import { DIMENSION_LABELS } from './DashboardFilterBar'
import type { DashboardFilterValues } from '@/api'

function readValues(search: string): DashboardFilterValues {
  let out: DashboardFilterValues = {}
  function Probe({ onValues }: { onValues: (v: DashboardFilterValues) => void }) {
    onValues(useFilterValues()[0])
    return null
  }
  render(
    <MemoryRouter initialEntries={[`/dashboards/1${search}`]}>
      <Probe onValues={v => { out = v }} />
    </MemoryRouter>,
  )
  return out
}

describe('useFilterValues', () => {
  it('reads an AI engine selection back from the URL', () => {
    expect(readValues('?ai_engine=GPT%20MT').ai_engine).toBe('GPT MT')
  })

  // The guard: a dimension added to the bar without a matching read here is
  // the same snap-back bug, one dimension later.
  it('reads back every dimension the filter bar offers', () => {
    const keys = Object.keys(DIMENSION_LABELS)
    const values = readValues('?' + keys.map(k => `${k}=v-${k}`).join('&'))
    for (const k of keys) {
      expect(values[k as keyof DashboardFilterValues], k).toBe(`v-${k}`)
    }
  })
})
