import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, listLLMDebugCalls, type LLMDebugEntry } from '@/api'

// Client-side cap mirrors the backend ring capacity — the page never holds
// more than the server can replay anyway.
const MAX_ENTRIES = 200
const POLL_MS = 1000

/**
 * Live feed for the /debug/llm page: 1s cursor polling against
 * GET /api/debug/llm/calls, accumulating entries client-side.
 *
 * Plain setInterval (not react-query): cursor accumulation + pause/clear are
 * log semantics, not cache semantics — there is no canonical "current value"
 * to cache/invalidate, just an append-only stream.
 */
export function useLLMDebugLog(): {
  /** Newest first, capped at 200. */
  entries: LLMDebugEntry[]
  paused: boolean
  setPaused: (p: boolean) => void
  /** Client-side: drop accumulated entries; the cursor keeps its place. */
  clear: () => void
  /**
   * Why polling stopped, if it did:
   *   'not-mounted' — 404: no dev ring and no persisted feed configured.
   *   'forbidden'   — 403: the production feed exists but is platform-owner
   *                   only, since its entries span every tenant.
   * null means we're polling normally.
   */
  unavailable: 'not-mounted' | 'forbidden' | null
} {
  const [entries, setEntries] = useState<LLMDebugEntry[]>([])
  const [paused, setPaused] = useState(false)
  const [unavailable, setUnavailable] = useState<'not-mounted' | 'forbidden' | null>(null)
  const cursorRef = useRef(0)
  const inFlightRef = useRef(false)

  useEffect(() => {
    if (paused || unavailable) return
    let cancelled = false

    const poll = async () => {
      if (inFlightRef.current) return
      inFlightRef.current = true
      try {
        const res = await listLLMDebugCalls(cursorRef.current)
        if (cancelled) return
        cursorRef.current = res.last_seq
        if (res.entries.length > 0) {
          setEntries(prev => [...res.entries].reverse().concat(prev).slice(0, MAX_ENTRIES))
        }
      } catch (err) {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 404) {
          // Route not mounted — stop polling entirely and show the hint.
          setUnavailable('not-mounted')
        } else if (err instanceof ApiError && err.status === 403) {
          // Production feed exists but this account isn't a platform owner.
          // Terminal like the 404: polling on would just repeat the refusal.
          setUnavailable('forbidden')
        }
        // Other errors (server restarting, network blip): keep polling.
      } finally {
        inFlightRef.current = false
      }
    }

    void poll()
    const id = setInterval(() => void poll(), POLL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [paused, unavailable])

  const clear = useCallback(() => setEntries([]), [])

  return { entries, paused, setPaused, clear, unavailable }
}
