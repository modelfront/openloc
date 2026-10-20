import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { openEventStream } from '../lib/event-stream'
import { queryKeys } from '../lib/query-keys'

/**
 * Refetches the job's segment holds the moment they change, from the
 * server's hold stream, so a segment someone opens shows as held at once in
 * every other editor of the job, and as free once they move on.
 *
 * Returns true while the stream is connected, so the caller can poll rarely;
 * false before the first `connected`, after a drop, over HTTP/1.1 in a window
 * without focus, and on a server that cannot stream.
 */
export function useHoldStream(jobId: number): boolean {
  const queryClient = useQueryClient()
  const [live, setLive] = useState(false)

  useEffect(() => {
    if (jobId <= 0) return
    const refetch = () => { void queryClient.invalidateQueries({ queryKey: queryKeys.job.holds(jobId) }) }
    // Only the window being worked in needs the stream; others keep the poll.
    const close = openEventStream(`/jobs/${jobId}/holds/stream`, {
      // A change may have been missed while the stream was down.
      connected: () => { setLive(true); refetch() },
      holds: refetch,
    }, { onDown: () => setLive(false), onlyWhileFocused: true })
    return () => {
      close()
      setLive(false)
    }
  }, [jobId, queryClient])

  return live
}
