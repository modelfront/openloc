import { BASE } from '../api/request'
import { notify, isEnabled } from './errtrack'

/** First pause before opening a stream the browser gave up on; doubles up to REOPEN_MAX_MS. */
const REOPEN_MIN_MS = 3_000
const REOPEN_MAX_MS = 60_000

/** True unless the page arrived over HTTP/2 or HTTP/3; unknown counts as limited. */
function connectionsAreLimited(): boolean {
  try {
    const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined
    const protocol = nav?.nextHopProtocol ?? ''
    return !(protocol === 'h2' || protocol.startsWith('h3'))
  } catch {
    return true
  }
}

export interface EventStreamOptions {
  /** Runs when the stream drops or this tab closes it; the server's next `connected` says it is back. */
  onDown?: () => void
  /**
   * Over HTTP/1.1, hold the stream only while the window has focus: for a
   * stream that only the window being worked in needs.
   */
  onlyWhileFocused?: boolean
}

/**
 * Opens a server-sent event stream on an API path and calls `on[name]` for
 * each named event. Returns a function that closes it for good.
 *
 * - Cookie auth: EventSource cannot set headers, so `withCredentials` sends
 *   the HttpOnly auth cookie, the same as any page-load request.
 * - The browser reconnects by itself after a drop, about 3 s later unless the
 *   server sent a `retry:` (the bell sends a random 1–20 s), but gives up for
 *   good on any answer other than 200 (a proxy's 502 during a deploy, a 204
 *   while the server cannot stream). Then the stream is opened again after a
 *   pause that doubles from REOPEN_MIN_MS to REOPEN_MAX_MS.
 * - Over HTTP/1.1 a browser allows about 6 connections per host, shared by
 *   every tab, and each open stream holds one: with enough tabs open, a new
 *   tab cannot load at all. There a hidden tab closes its stream and reopens
 *   it when shown, a tab opened in the background waits until it is shown,
 *   and `onlyWhileFocused` narrows that to the window with focus. HTTP/2 and
 *   HTTP/3 share one connection, so tabs keep theirs.
 */
export function openEventStream(path: string, on: Record<string, () => void>, opts: EventStreamOptions = {}): () => void {
  const url = `${BASE}${path}`
  const limited = connectionsAreLimited()
  let es: EventSource | null = null
  let reopenTimer: ReturnType<typeof setTimeout> | null = null
  let reopenDelay = REOPEN_MIN_MS

  const wanted = () => !limited
    || (document.visibilityState !== 'hidden' && (!opts.onlyWhileFocused || document.hasFocus()))

  const reopenLater = () => {
    if (reopenTimer != null) return
    const delay = reopenDelay
    reopenDelay = Math.min(delay * 2, REOPEN_MAX_MS)
    reopenTimer = setTimeout(() => {
      reopenTimer = null
      if (wanted()) open()
    }, delay)
  }

  // Returns false when the browser refuses EventSource altogether.
  const open = (): boolean => {
    if (es) return true
    let current: EventSource
    try {
      current = new EventSource(url, { withCredentials: true })
    } catch (err) {
      reportUnavailable(err)
      return false
    }
    es = current
    for (const [name, fn] of Object.entries(on)) current.addEventListener(name, fn)
    current.onopen = () => { reopenDelay = REOPEN_MIN_MS }
    // Not reported: it fires every ~3 s during any outage.
    current.onerror = () => {
      opts.onDown?.()
      if (current.readyState === EventSource.CLOSED && es === current) {
        es = null
        reopenLater()
      }
    }
    return true
  }
  const close = () => {
    if (reopenTimer != null) clearTimeout(reopenTimer)
    reopenTimer = null
    if (!es) return
    es.close()
    es = null
    opts.onDown?.()
  }
  const sync = () => { if (wanted()) open(); else close() }

  const available = !wanted() || open()
  const watchFocus = available && limited && !!opts.onlyWhileFocused
  if (available && limited) document.addEventListener('visibilitychange', sync)
  if (watchFocus) {
    window.addEventListener('focus', sync)
    window.addEventListener('blur', sync)
  }

  return () => {
    document.removeEventListener('visibilitychange', sync)
    window.removeEventListener('focus', sync)
    window.removeEventListener('blur', sync)
    if (reopenTimer != null) clearTimeout(reopenTimer)
    reopenTimer = null
    es?.close()
    es = null
  }
}

// Browser blocked EventSource (very old or strict CSP); the callers' polls
// remain. Reported once per session: the failure is environmental, so repeats
// add noise, not information.
function reportUnavailable(err: unknown): void {
  try {
    // Gate on isEnabled(): notify() is a no-op until /api/config resolves, and
    // setting the dedupe flag before a real report would swallow it for the
    // whole session.
    if (isEnabled() && !sessionStorage.getItem('errtrack:es-ctor-reported')) {
      sessionStorage.setItem('errtrack:es-ctor-reported', '1')
      notify(err, { name: 'EventSourceUnavailable', fingerprint: 'eventsource-ctor' })
    }
  } catch {
    // sessionStorage blocked too — skip the dedupe rather than spam.
  }
}
