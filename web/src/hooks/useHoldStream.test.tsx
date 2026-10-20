import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useHoldStream } from './useHoldStream'
import { queryKeys } from '../lib/query-keys'
import { MockEventSource, installMockEventSource, uninstallMockEventSource } from '../test/mock-event-source'

// The editor window has focus; jsdom reports none, and no HTTP/2.
beforeEach(() => {
  installMockEventSource()
  vi.spyOn(document, 'hasFocus').mockReturnValue(true)
})
afterEach(() => {
  vi.restoreAllMocks()
  uninstallMockEventSource()
})

function setup(jobId = 7) {
  const qc = new QueryClient()
  const invalidate = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  const view = renderHook(({ id }) => useHoldStream(id), { wrapper, initialProps: { id: jobId } })
  const holdRefetches = () => invalidate.mock.calls.filter(([arg]) =>
    JSON.stringify((arg as { queryKey?: unknown })?.queryKey) === JSON.stringify(queryKeys.job.holds(jobId))).length
  return { ...view, holdRefetches }
}

describe('useHoldStream', () => {
  it("opens the job's hold stream with the auth cookie", () => {
    setup()
    expect(MockEventSource.instances).toHaveLength(1)
    expect(MockEventSource.instances[0].url).toMatch(/\/jobs\/7\/holds\/stream$/)
    expect(MockEventSource.instances[0].withCredentials).toBe(true)
  })

  it('refetches the holds on every change the server announces', () => {
    const { holdRefetches } = setup()
    const es = MockEventSource.instances[0]
    act(() => es.emit('connected', {}))
    const afterConnect = holdRefetches()
    expect(afterConnect).toBe(1)
    act(() => es.emit('holds', {}))
    act(() => es.emit('holds', {}))
    expect(holdRefetches()).toBe(afterConnect + 2)
  })

  it('is live from `connected` until the connection drops', () => {
    const { result } = setup()
    const es = MockEventSource.instances[0]
    expect(result.current).toBe(false)
    act(() => es.emit('connected', {}))
    expect(result.current).toBe(true)
    act(() => es.fail())
    expect(result.current).toBe(false)
    act(() => es.emit('connected', {}))
    expect(result.current).toBe(true)
  })

  it('closes the stream on unmount and when the job changes', () => {
    const { rerender, unmount } = setup()
    const first = MockEventSource.instances[0]
    rerender({ id: 8 })
    expect(first.closed).toBe(true)
    expect(MockEventSource.instances[1].url).toMatch(/\/jobs\/8\/holds\/stream$/)
    unmount()
    expect(MockEventSource.instances[1].closed).toBe(true)
  })

  it('over HTTP/1.1 a window without focus holds no stream and is not live', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    const { result } = setup()
    expect(MockEventSource.instances).toHaveLength(0)
    expect(result.current).toBe(false)
  })

  it('opens nothing before the job is known', () => {
    setup(0)
    expect(MockEventSource.instances).toHaveLength(0)
  })
})
