import { describe, it, expect, vi } from 'vitest'
import { loadFailure, type FailableQuery } from './load-failure'

const query = (over: Partial<FailableQuery> = {}): FailableQuery => ({
  isError: false, data: undefined, error: null, refetch: vi.fn(), ...over,
})

describe('loadFailure', () => {
  it('is null when nothing failed', () => {
    expect(loadFailure(query({ data: [] }), query({ data: [1] }))).toBeNull()
  })

  it('ignores a failed background refetch that still has data', () => {
    expect(loadFailure(query({ isError: true, data: [], error: new Error('stale') }))).toBeNull()
  })

  it('reports the first failed query and retries every failed one', () => {
    const ok = query({ data: [] })
    const a = query({ isError: true, error: new Error('a down') })
    const b = query({ isError: true, error: new Error('b down') })

    const f = loadFailure(ok, a, b)

    expect(f?.error).toEqual(new Error('a down'))
    f?.retry()
    expect(a.refetch).toHaveBeenCalledOnce()
    expect(b.refetch).toHaveBeenCalledOnce()
    expect(ok.refetch).not.toHaveBeenCalled()
  })
})
