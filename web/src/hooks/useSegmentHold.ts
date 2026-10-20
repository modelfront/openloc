import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { acquireHold, listHolds, releaseHold } from '../api/holds'
import { ApiError } from '../api/request'
import { queryKeys } from '../lib/query-keys'
import { isSegmentHeldError } from '../lib/save-conflict'
import { applyAcquiredRow } from '../lib/segment-save'
import { useHoldStream } from './useHoldStream'

/** Renewal interval; well inside the server's 90 s hold lifetime. */
export const HOLD_RENEW_MS = 20_000
/** How often the editor reads who holds which segment without the live stream. */
export const HOLD_POLL_MS = 5_000
/**
 * The same while the stream is live. It still runs because a hold that
 * expires (a closed laptop, a crashed tab) ends without a change to announce.
 */
const HOLD_POLL_LIVE_MS = 30_000
/** No input for this long closes the active segment, which frees it. */
const HOLD_IDLE_MS = 5 * 60_000
/** First retry after a failed request the server did not refuse; doubles up to HOLD_RENEW_MS. */
const HOLD_RETRY_MS = 1_000

const ACTIVITY_EVENTS = ['keydown', 'pointerdown'] as const

type Blocked = { translationId: number; name: string }

// A per-page-load id. crypto.randomUUID exists only in secure contexts, and a
// self-hosted deployment may be opened over plain HTTP.
function newTabId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
}

// No answer, or a server error: the request may not have reached the server,
// so what it holds for this tab is unknown.
function isTransient(err: unknown): boolean {
  return !(err instanceof ApiError) || err.status >= 500
}

function holderName(err: unknown): string {
  const body = err instanceof ApiError ? (err.body as { holder?: { name?: unknown } } | undefined) : undefined
  return typeof body?.holder?.name === 'string' ? body.holder.name : 'Someone'
}

/**
 * Holds the active segment for this tab while it is open, so other users see
 * it read-only with this user's name, and reports who holds the other
 * segments of the job. The server's hold stream refetches the list the moment
 * it changes; the poll is the fallback.
 *
 * One request is in flight at a time and each sends the latest wanted state,
 * so fast navigation, StrictMode and several server replicas cannot reorder
 * a release after an acquire. The hold is renewed every HOLD_RENEW_MS, on
 * returning to the tab and on `pageshow`; it is released on close, on unmount
 * and (keepalive) on `pagehide`. After HOLD_IDLE_MS without input `onIdle`
 * runs; the editor closes the segment, which flushes and releases.
 * `streamLive` is true while the hold stream is connected.
 */
export function useSegmentHold(args: {
  jobId: number
  /** The active row's translation; null when no row is active. */
  translationId: number | null
  /** The user may edit this row now: an editable task and not locked. */
  canHold: boolean
  currentUserId: number | null
  onIdle: () => void
}): { heldByOthers: Map<number, string>; streamLive: boolean } {
  const { jobId, translationId, canHold, currentUserId, onIdle } = args
  const queryClient = useQueryClient()
  const [tabId] = useState(newTabId)
  const wanted = canHold ? translationId : null

  const wantedRef = useRef<number | null>(wanted)
  const heldRef = useRef<number | null>(null)
  const busyRef = useRef(false)
  const againRef = useRef(false)
  const lastInputRef = useRef(Date.now())
  // Unmounted or the page hidden for good: hold nothing, and release any
  // acquire that resolves afterwards.
  const suspendedRef = useRef(false)
  const onIdleRef = useRef(onIdle)
  onIdleRef.current = onIdle
  const [blocked, setBlocked] = useState<Blocked | null>(null)
  const blockedRef = useRef<Blocked | null>(null)
  blockedRef.current = blocked
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const retryDelayRef = useRef(HOLD_RETRY_MS)
  const syncRef = useRef<() => Promise<void>>(async () => {})

  const release = useCallback((id: number, opts?: { keepalive: boolean }) =>
    (opts ? releaseHold(jobId, id, tabId, opts) : releaseHold(jobId, id, tabId))
      .catch(err => console.warn('segment hold release failed:', err)), [jobId, tabId])

  const sync = useCallback(async () => {
    if (busyRef.current) {
      againRef.current = true
      return
    }
    busyRef.current = true
    try {
      do {
        againRef.current = false
        const want = suspendedRef.current ? null : wantedRef.current
        if (want != null) {
          try {
            const got = await acquireHold(jobId, want, tabId)
            if (suspendedRef.current) {
              await release(want, { keepalive: true })
              continue
            }
            heldRef.current = want
            retryDelayRef.current = HOLD_RETRY_MS
            setBlocked(b => (b ? null : b))
            applyAcquiredRow(queryClient, jobId, got.translation)
          } catch (err) {
            if (isTransient(err)) {
              // The server may still hold the tab's segment, so a close still
              // releases it; try again soon rather than at the next renewal.
              heldRef.current = heldRef.current ?? want
              console.warn('segment hold failed, retrying:', err)
              if (retryTimerRef.current == null) {
                const delay = retryDelayRef.current
                retryDelayRef.current = Math.min(delay * 2, HOLD_RENEW_MS)
                retryTimerRef.current = setTimeout(() => {
                  retryTimerRef.current = null
                  void syncRef.current()
                }, delay)
              }
            } else {
              // A refusal: the server moved this tab off its previous segment.
              heldRef.current = null
              if (isSegmentHeldError(err)) setBlocked({ translationId: want, name: holderName(err) })
              else console.warn('segment hold failed:', err)
            }
          }
        } else if (heldRef.current != null) {
          const prev = heldRef.current
          heldRef.current = null
          await release(prev)
        }
      } while (againRef.current)
    } finally {
      busyRef.current = false
    }
  }, [jobId, tabId, queryClient, release])

  useEffect(() => { syncRef.current = sync }, [sync])

  useEffect(() => {
    wantedRef.current = wanted
    setBlocked(b => (b && b.translationId !== wanted ? null : b))
    void sync()
  }, [wanted, sync])

  // Renewal and the idle limit.
  useEffect(() => {
    const onInput = () => { lastInputRef.current = Date.now() }
    for (const e of ACTIVITY_EVENTS) window.addEventListener(e, onInput, true)
    const timer = setInterval(() => {
      if (wantedRef.current == null) return
      if (Date.now() - lastInputRef.current >= HOLD_IDLE_MS) {
        onIdleRef.current()
        return
      }
      void sync()
    }, HOLD_RENEW_MS)
    return () => {
      clearInterval(timer)
      for (const e of ACTIVITY_EVENTS) window.removeEventListener(e, onInput, true)
    }
  }, [sync])

  // Back to the tab, page restored from the back/forward cache, page leaving.
  useEffect(() => {
    const onVisible = () => { if (document.visibilityState === 'visible') void sync() }
    const onShow = (e: PageTransitionEvent) => {
      if (!e.persisted) return
      suspendedRef.current = false
      void sync()
    }
    const onHide = () => {
      suspendedRef.current = true
      const held = heldRef.current
      if (held == null) return
      heldRef.current = null
      void release(held, { keepalive: true })
    }
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('pageshow', onShow)
    window.addEventListener('pagehide', onHide)
    return () => {
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('pageshow', onShow)
      window.removeEventListener('pagehide', onHide)
    }
  }, [sync, release])

  // Leaving the editor releases the hold, and any acquire still in flight.
  useEffect(() => {
    suspendedRef.current = false
    return () => {
      suspendedRef.current = true
      if (retryTimerRef.current != null) clearTimeout(retryTimerRef.current)
      retryTimerRef.current = null
      const held = heldRef.current
      if (held == null) return
      heldRef.current = null
      void release(held)
    }
  }, [release])

  const streamLive = useHoldStream(jobId)
  const { data: holds, dataUpdatedAt } = useQuery({
    queryKey: queryKeys.job.holds(jobId),
    queryFn: () => listHolds(jobId),
    refetchInterval: streamLive ? HOLD_POLL_LIVE_MS : HOLD_POLL_MS,
    enabled: jobId > 0,
  })

  // Blocked, and a new poll no longer shows anyone else on that segment: take
  // it. Keyed on the poll, so a refusal never retries against the same data.
  useEffect(() => {
    const b = blockedRef.current
    if (!b || !holds) return
    const stillHeld = holds.some(h => h.translation_id === b.translationId && h.user_id !== currentUserId)
    if (!stillHeld) void sync()
  }, [dataUpdatedAt]) // eslint-disable-line react-hooks/exhaustive-deps -- only a new poll result retries

  const heldByOthers = useMemo(() => {
    const m = new Map<number, string>()
    for (const h of holds ?? []) {
      if (h.user_id !== currentUserId) m.set(h.translation_id, h.holder_name)
    }
    // A refused acquire shows at once, before the next poll catches up.
    if (blocked && !m.has(blocked.translationId)) m.set(blocked.translationId, blocked.name)
    return m
  }, [holds, blocked, currentUserId])

  return { heldByOthers, streamLive }
}
