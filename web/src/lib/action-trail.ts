// The action trail: the last things that happened in the app before a report —
// page changes, clicks, failed API calls, console errors and notices. It helps
// the support team see what led up to a problem. It lives in memory only, is
// never stored in the browser, never holds typed text, and goes with an issue
// report to the support team alone: no model ever reads it.
import type { RecordingEvent, TrailEvent } from '@/api'
import { cleanIssuePath, onSupportPage } from './issue-context'

type NewTrailEvent = TrailEvent extends infer E ? (E extends TrailEvent ? Omit<E, 'at'> : never) : never

/** The server keeps the same caps. */
export const TRAIL_MAX_EVENTS = 50
const MAX_TARGET = 60
const MAX_MESSAGE = 200

const events: TrailEvent[] = []

/** s on one line, cut to n characters. */
export const cap = (s: string, n: number) => {
  const flat = s.replace(/\s+/g, ' ').trim()
  return flat.length > n ? `${flat.slice(0, n - 1)}…` : flat
}

const trailListeners = new Set<(e: TrailEvent) => void>()

/** Hears every event added to the trail while subscribed; returns the unsubscribe. */
export function onTrailEvent(listener: (e: TrailEvent) => void): () => void {
  trailListeners.add(listener)
  return () => { trailListeners.delete(listener) }
}

/** Adds an event, dropping the oldest past the cap. Nothing on the support team's pages. */
export function recordTrail(e: NewTrailEvent): void {
  if (onSupportPage()) return
  const event = { ...e, at: new Date().toISOString() } as TrailEvent
  if (event.kind === 'click') event.target = cap(event.target, MAX_TARGET)
  if (event.kind === 'console' || event.kind === 'toast') event.message = cap(event.message, MAX_MESSAGE)
  if (event.kind === 'click' && !event.target) return
  events.push(event)
  if (events.length > TRAIL_MAX_EVENTS) events.splice(0, events.length - TRAIL_MAX_EVENTS)
  for (const l of trailListeners) l(event)
}

/** The trail so far, oldest first. */
export function actionTrail(): TrailEvent[] {
  return events.slice()
}

/** Empties the trail (tests). */
export function clearActionTrail(): void {
  events.length = 0
}

/** Controls whose click says what the user did. Text fields are not among them: their value is typed text. */
const CONTROLS = [
  'a[href]', 'button', 'summary', 'input[type="checkbox"]', 'input[type="radio"]',
  ...['button', 'link', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'option', 'radio', 'checkbox', 'switch']
    .map(r => `[role="${r}"]`),
].join(', ')

/**
 * What a click was on: the control's label, else the nearest data-guide id.
 * A list option says only that it was one, since a "create" option shows what
 * the user typed. Nothing inside an element marked data-private.
 */
export function clickTarget(el: Element | null): string | undefined {
  if (!el || el.closest('[data-private]')) return undefined
  const control = el.closest(CONTROLS)
  if (control?.getAttribute('role') === 'option') return 'a list option'
  const label = control && (control.getAttribute('aria-label') || control.getAttribute('title') || control.textContent || '')
  const guide = el.closest('[data-guide]')?.getAttribute('data-guide') ?? ''
  const target = cap(label || guide, MAX_TARGET)
  return target || undefined
}

/**
 * A long segment of letters and digits together is a token or an upload id,
 * not a route name: route names have no digits.
 */
const isSecretSegment = (s: string) => /^[A-Za-z0-9_-]{20,}$/.test(s) && /\d/.test(s) && /[A-Za-z]/.test(s)

/** The path of a same-origin API call, without the query or any token; undefined for anything else. */
export function trailApiPath(url: string): string | undefined {
  let u: URL
  try {
    u = new URL(url, window.location.origin)
  } catch {
    return undefined
  }
  if (u.origin !== window.location.origin || !u.pathname.startsWith('/api/')) return undefined
  return u.pathname.split('/').map(s => (isSecretSegment(s) ? ':token' : s)).join('/')
}

/** The page path as a report keeps it: no query, no fragment, no secret segment. */
export const trailPagePath = cleanIssuePath

const errorText = (v: unknown) => (v instanceof Error ? v.message : typeof v === 'string' ? v : String(v))

/** A failed API call, reported to the listeners of a recording (watch mode). */
type ApiFailureListener = (failure: { method: string; path: string; response?: Response }) => void

const apiFailureListeners = new Set<ApiFailureListener>()

/** Hears every failed API call while subscribed; returns the unsubscribe. */
export function onApiFailure(listener: ApiFailureListener): () => void {
  apiFailureListeners.add(listener)
  return () => { apiFailureListeners.delete(listener) }
}

let installs = 0
let uninstall: (() => void) | null = null

/**
 * Starts recording clicks, failed API calls and console errors. Page changes
 * and notices come from ActionTrailRecorder, which calls this. Returns the
 * stop function, which acts once; nested calls share one recorder.
 */
export function installActionTrail(): () => void {
  installs++
  if (installs === 1) uninstall = start()
  let stopped = false
  return () => {
    if (stopped) return
    stopped = true
    installs--
    if (installs === 0) {
      uninstall?.()
      uninstall = null
    }
  }
}

function start(): () => void {
  const onClick = (e: MouseEvent) => {
    const target = clickTarget(e.target instanceof Element ? e.target : null)
    if (target) recordTrail({ kind: 'click', target })
  }
  document.addEventListener('click', onClick, true)

  const originalFetch = window.fetch
  const failed = (method: string, path: string, status: number, response?: Response) => {
    recordTrail({ kind: 'api', method, path, status })
    for (const l of apiFailureListeners) l({ method, path, response })
  }
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase()
    const path = trailApiPath(url)
    try {
      const res = await originalFetch.call(window, input, init)
      if (!res.ok && path) failed(method, path, res.status, res)
      return res
    } catch (err) {
      if (path && (err as Error)?.name !== 'AbortError') failed(method, path, 0)
      throw err
    }
  }

  const originalError = console.error
  console.error = (...args: unknown[]) => {
    recordTrail({ kind: 'console', message: args.map(errorText).join(' ') })
    originalError.apply(console, args)
  }
  const onError = (e: ErrorEvent) => recordTrail({ kind: 'console', message: e.message || errorText(e.error) })
  const onRejection = (e: PromiseRejectionEvent) => recordTrail({ kind: 'console', message: errorText(e.reason) })
  window.addEventListener('error', onError)
  window.addEventListener('unhandledrejection', onRejection)

  return () => {
    document.removeEventListener('click', onClick, true)
    window.fetch = originalFetch
    console.error = originalError
    window.removeEventListener('error', onError)
    window.removeEventListener('unhandledrejection', onRejection)
  }
}

/** One line for an event of a trail or a recording, as the report form and the support team show it. */
export function trailText(e: RecordingEvent): string {
  switch (e.kind) {
    case 'input': return `Typed “${e.value}” in ${e.target}`
    case 'page': return `Opened ${e.path}`
    case 'click': return `Clicked “${e.target}”`
    case 'api': return `${e.method} ${e.path} failed (${e.status || 'no response'})`
    case 'console': return `Console error: ${e.message}`
    case 'toast': return `${e.level === 'error' ? 'Error message' : 'Message'}: ${e.message}`
  }
}
