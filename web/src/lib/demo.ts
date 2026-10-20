// The shared, read-only demo organization.
import { toast } from 'sonner'
import { ApiError, enterDemo, leaveDemo, type UserProfile } from '../api'

/** The parameters naming a saved tour: `?tour=` (older links) and the running tour's hash. */
export const TOUR_PARAM = 'tour'
/** The step, counted from 1. */
export const TOUR_STEP_PARAM = 'step'

/**
 * The entry address of a saved tour at a step (0-based index): /tour/<name>/<n>.
 * TourEntryPage shows no organization's data; it enters the demo when needed
 * and then opens the step's own page.
 */
export function tourPath(tour: string, start = 0): string {
  return `/tour/${encodeURIComponent(tour)}/${start + 1}`
}

// While a saved tour runs, the address bar carries it after "#" —
// #tour=<name>&step=<n> — so the URL can be copied at any step and a reload
// continues it. The hash, not the query: pages keep their own state in the
// query (filters), and no page reads the hash. history.state is kept as it
// is: the router stores its own entry data there.
const HASH_PREFIX = '#tour='

export function setTourHash(tour: string, index: number) {
  const hash = `${HASH_PREFIX}${encodeURIComponent(tour)}&${TOUR_STEP_PARAM}=${index + 1}`
  if (window.location.hash === hash) return
  window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search + hash)
}

export function clearTourHash() {
  if (!window.location.hash.startsWith(HASH_PREFIX)) return
  window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search)
}

/** The saved tour and 0-based step in the address bar's hash, if any. */
export function tourFromHash(hash: string): { tour: string; start: number } | null {
  if (!hash.startsWith(HASH_PREFIX)) return null
  const q = new URLSearchParams(hash.slice(1))
  const tour = q.get(TOUR_PARAM)
  if (!tour) return null
  return { tour, start: Math.max((Number(q.get(TOUR_STEP_PARAM)) || 1) - 1, 0) }
}

/**
 * Enters the demo organization and reloads, so every query refetches under
 * the demo token. With a tour name, that tour starts once the page is back,
 * at `start` (a 0-based step index) when given.
 */
export async function goToDemo(tour?: string, start = 0): Promise<void> {
  await enterDemo()
  window.location.href = tour ? tourPath(tour, start) : '/projects'
}

/** Leaves the demo: back to the org the visitor came from, or to the org picker. */
export async function goBackFromDemo(): Promise<void> {
  const res = await leaveDemo()
  window.location.href = res.needs_org_selection ? '/select-org' : '/'
}

/** The button that leaves the demo: back to the org the visitor came from, when there is one. */
export function demoLeaveLabel(me: Pick<UserProfile, 'demo_return_org_name'> | undefined): string {
  const back = me?.demo_return_org_name
  return back ? `Back to ${back}` : 'Leave the Playground'
}

/** Leaves the demo, with a toast when that fails. */
export function leavePlayground() {
  goBackFromDemo().catch((err: Error) => toast.error(`Could not leave the Playground: ${err.message}`))
}

/** A saved tour in the Playground ended; the Playground banner offers the way back. */
export const PLAYGROUND_TOUR_END_EVENT = 'openloc:playground-tour-end'

/** `finished` is true after Done on the last step, false when the tour was closed early. */
export function announcePlaygroundTourEnd(finished: boolean) {
  window.dispatchEvent(new CustomEvent<boolean>(PLAYGROUND_TOUR_END_EVENT, { detail: finished }))
}

/** The server refused a change because this is the demo organization. */
export function isDemoReadOnly(err: unknown): boolean {
  if (!(err instanceof ApiError) || err.status !== 403) return false
  const body = err.body as { error?: { code?: string } } | undefined
  return body?.error?.code === 'demo_read_only'
}

export const DEMO_READ_ONLY_MESSAGE = 'This is the Playground. Changes won’t be saved.'

/**
 * The one notice for a change the demo refused. Returns true when err was
 * such a refusal (the caller shows nothing else).
 */
export function notifyDemoReadOnly(err: unknown, show: (message: string) => void): boolean {
  if (!isDemoReadOnly(err)) return false
  show(DEMO_READ_ONLY_MESSAGE)
  return true
}
