import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { openEventStream } from '../lib/event-stream'
import { queryKeys } from '../lib/query-keys'
import type { EditorId } from '../lib/editor-scope'
import { holdsPath } from '../api/holds'

/**
 * Refetches the job's segment holds the moment they change, from the
 * server's hold stream, so a segment someone opens shows as held at once in
 * every other editor of the job, and as free once they move on.
 *
 * Returns true while the stream is connected, so the caller can poll rarely;
 * false before the first `connected`, after a drop, over HTTP/1.1 in a window
 * without focus, and on a server that cannot stream.
 */
export function useHoldStream(editorId: EditorId): boolean {
  const queryClient = useQueryClient()
  const [live, setLive] = useState(false)

  useEffect(() => {
    if (editorId === 0) return
    const refetch = () => { void queryClient.invalidateQueries({ queryKey: queryKeys.job.holds(editorId) }) }
    // Only the window being worked in needs the stream; others keep the poll.
    const close = openEventStream(holdsPath(editorId, true), {
      // A change may have been missed while the stream was down.
      connected: () => { setLive(true); refetch() },
      holds: refetch,
    }, { onDown: () => setLive(false), onlyWhileFocused: true })
    return () => {
      close()
      setLive(false)
    }
  }, [editorId, queryClient])

  return live
}
