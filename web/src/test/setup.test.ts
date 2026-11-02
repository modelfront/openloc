import { describe, it, expect } from 'vitest'
import { QueryClient, QueryObserver, notifyManager } from '@tanstack/react-query'

describe('test setup', () => {
  // A query update reaches React before the next timer. On React Query's
  // default 0 ms timer, an update queued at the end of the last test in a file
  // can fire after jsdom is torn down, and React then throws "window is not
  // defined", blamed on whichever file ran last. useQuery subscribes through
  // notifyManager.batchCalls, so this test does too.
  it('delivers React Query updates without waiting for a timer', async () => {
    const qc = new QueryClient()
    const seen: unknown[] = []
    const observer = new QueryObserver(qc, { queryKey: ['k'], enabled: false })
    const unsubscribe = observer.subscribe(notifyManager.batchCalls(result => seen.push(result.data)))

    qc.setQueryData(['k'], 'v')
    await Promise.resolve()

    unsubscribe()
    qc.clear()
    expect(seen).toContain('v')
  })
})
