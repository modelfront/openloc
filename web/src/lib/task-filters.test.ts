import { describe, it, expect } from 'vitest'
import { parseTaskFilters } from './task-filters'

describe('parseTaskFilters', () => {
  it('reads every filter', () => {
    expect(parseTaskFilters(new URLSearchParams('q=manual&step=Review&deadline=overdue')))
      .toEqual({ step: 'Review', deadline: 'overdue', q: 'manual' })
  })

  it('drops an unknown deadline bucket and empty values', () => {
    expect(parseTaskFilters(new URLSearchParams('deadline=tomorrow&step=&q='))).toEqual({})
  })

  it('builds the same key whatever order the URL lists the params in', () => {
    const a = JSON.stringify(parseTaskFilters(new URLSearchParams('q=x&deadline=24h&step=Review')))
    const b = JSON.stringify(parseTaskFilters(new URLSearchParams('step=Review&q=x&deadline=24h')))
    expect(a).toBe(b)
  })
})
