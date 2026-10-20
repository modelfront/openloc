import Honeybadger from '@honeybadger-io/js'

// ─── Frontend error reporting → Honeybadger, via the backend proxy ──────────
//
// The browser twin of internal/errtrack, following the same rules:
// fail-safe off by default, scrub before anything leaves, quota discipline.
// Identity is NOT ours: the proxy replaces it from the session cookie — see
// setIdentity below.
//
// No API key ever reaches the browser. The SDK is configured with a
// placeholder key and endpoint=<origin>/api/errtrack; the backend proxy
// (internal/api/errtrack_proxy.go) attaches the real key, scrubs again
// server-side, and forwards to Honeybadger. GET /api/config only tells us
// whether reporting is on (error_reporting boolean) plus environment/revision.
//
// Breadcrumbs are disabled: the SDK attaches them AFTER beforeNotify handlers
// run, so they cannot be scrubbed client-side — and they record full URLs,
// which can carry customer segment text (?source_text=). The proxy drops any
// that slip through.

let enabled = false

/** True once /api/config confirmed reporting is on and the SDK is configured. */
export function isEnabled(): boolean {
  return enabled
}

export async function initErrtrack(): Promise<void> {
  try {
    const res = await fetch('/api/config')
    if (!res.ok) return
    const cfg = (await res.json()) as {
      version?: string
      environment?: string
      error_reporting?: boolean
    }
    if (!cfg.error_reporting) return
    Honeybadger.configure({
      // Placeholder — the SDK refuses to send without a key; the backend
      // proxy discards this value and attaches the real one.
      apiKey: 'proxied',
      endpoint: window.location.origin + '/api/errtrack',
      environment: cfg.environment,
      revision: cfg.version,
      // We gate on the error_reporting flag, not on environment names, so a
      // future "staging2" doesn't silently disable reporting.
      reportData: true,
      // Hard cap per page load. NOTE: the SDK increments its counter in a
      // built-in beforeNotify that runs BEFORE our throttle, so auto-caught
      // errors dropped by the throttle still burn budget — which is why this
      // is 25, not 10: a throttled error loop must not mute later, distinct
      // errors. Our own notify() pre-checks the throttle and never reaches
      // the SDK when dropped, so manual notices don't have that problem.
      maxErrors: 25,
      breadcrumbsEnabled: false,
      enableUncaught: true,
      // Off: the SDK's hook reports every rejection, expected ones too.
      // handleUnhandledRejection sends them through shouldReport instead.
      enableUnhandledRejection: false,
    })
    Honeybadger.beforeNotify(scrubNotice)
    Honeybadger.beforeNotify(throttleNotice)
    window.addEventListener('unhandledrejection', handleUnhandledRejection)
    enabled = true
  } catch {
    // Config fetch failed — reporting stays off. Fail-safe, like the backend.
  }
}

export interface NotifyOptions {
  /** Error class shown in Honeybadger (e.g. 'ReactRenderError'). */
  name?: string
  context?: Record<string, unknown>
  fingerprint?: string
}

// True while a notify() call that already consumed a throttle token is inside
// Honeybadger.notify — the SDK runs beforeNotify handlers synchronously, so
// this flag tells the registered throttleNotice handler not to consume a
// second token for the same notice. Without it every manual notice would be
// double-counted, halving the effective budget.
let throttlePrepaid = false

/** Report an error. No-op unless enabled; never throws. */
export function notify(err: unknown, opts: NotifyOptions = {}): void {
  if (!enabled) return
  try {
    const error = toError(err)
    // Throttle BEFORE handing to the SDK: a notice the SDK drops in
    // beforeNotify still counts against its maxErrors page budget (the
    // built-in counter increments first), so dropped notices must never
    // reach it.
    if (!throttleNotice({ name: opts.name ?? error.name, message: error.message })) return
    throttlePrepaid = true
    Honeybadger.notify(error, {
      ...(opts.name ? { name: opts.name } : {}),
      ...(opts.context ? { context: sanitizeContext(opts.context) } : {}),
      ...(opts.fingerprint ? { fingerprint: opts.fingerprint } : {}),
    })
  } catch {
    // Reporting must never break the app.
  } finally {
    throttlePrepaid = false
  }
}

/**
 * Report an error, then run `action` once delivery finished — or after
 * `timeoutMs` if it didn't. For report-before-reload paths where a
 * window.location.reload() would cancel the in-flight notice.
 */
export function notifyThen(err: unknown, opts: NotifyOptions, action: () => void, timeoutMs = 1500): void {
  // 1500ms, not something snappier: the SDK's transport is a plain fetch
  // without keepalive, so the action (typically a reload) cancels an
  // in-flight POST — and a cold-connection POST routinely needs >300ms.
  // The delay is paid once, in an already-broken moment.
  if (!enabled) {
    action()
    return
  }
  let done = false
  const run = () => {
    if (done) return
    done = true
    action()
  }
  const timer = setTimeout(run, timeoutMs)
  try {
    const error = toError(err)
    if (!throttleNotice({ name: opts.name ?? error.name, message: error.message })) {
      clearTimeout(timer)
      run()
      return
    }
    throttlePrepaid = true
    Honeybadger.notify(error, {
      ...(opts.name ? { name: opts.name } : {}),
      ...(opts.context ? { context: sanitizeContext(opts.context) } : {}),
      ...(opts.fingerprint ? { fingerprint: opts.fingerprint } : {}),
      afterNotify: () => {
        clearTimeout(timer)
        run()
      },
    })
  } catch {
    clearTimeout(timer)
    run()
  } finally {
    throttlePrepaid = false
  }
}

/** A promise rejected with nobody catching it: reported unless it is an expected failure. */
export function handleUnhandledRejection(event: PromiseRejectionEvent): void {
  if (!shouldReport(event.reason)) return
  notify(event.reason, { name: 'UnhandledRejection' })
}

function toError(err: unknown): Error {
  if (err instanceof Error) return err
  return new Error(typeof err === 'string' ? err : JSON.stringify(err))
}

// ─── Filtering ───────────────────────────────────────────────────────────────

/**
 * Whether a query/mutation error is worth a notice. Pure so the matrix is
 * unit-testable. Duck-types on error name/status instead of importing
 * ApiError/NoClaudeKeyError — keeps this module import-cycle-free (request.ts
 * imports us for clearIdentity on logout).
 */
export function shouldReport(err: unknown): boolean {
  if (err == null) return false
  const e = err as { name?: unknown; message?: unknown; status?: unknown }
  const name = typeof e.name === 'string' ? e.name : ''
  // User navigation / manual cancel (chat abort, unmounted queries).
  if (name === 'AbortError' || name === 'CanceledError') return false
  // Stale-chunk failures have their own report-then-reload path (lazy-reload).
  if (isChunkErrorMessage(e.message)) return false
  // Expected "Claude key not configured" state (HTTP 412 in api/chat.ts).
  if (name === 'NoClaudeKeyError') return false
  // request.ts throws a plain Error for the 401 → logout path.
  if (e.message === 'Session expired') return false
  if (name === 'ApiError' && typeof e.status === 'number') {
    const s = e.status
    // Status 0 is the XHR sentinel for "no HTTP response at all" — a real
    // connectivity failure, exactly what the backend can't see.
    if (s === 0) return true
    // The backend already reported its own 5xx to the same Honeybadger
    // project — a second, less-informative notice from here is pure noise.
    if (s >= 500) return false
    // Expected client statuses: session expiry, RBAC, stale links,
    // optimistic-locking conflicts (handled UI), missing Claude key, and a
    // rate limit, whose message tells the user to wait.
    if (s === 401 || s === 403 || s === 404 || s === 409 || s === 412 || s === 429) return false
    // Unexpected 4xx (400, 405, 413, 422…) = client/server contract bug.
    return true
  }
  return true
}

// Mirrors the signature list in lazy-reload.ts's isChunkLoadError without
// importing it — lazy-reload imports this module, so importing back would be
// a cycle. Keep the two lists in sync. Only the message check is needed
// here — name-based ChunkLoadError never reaches query/mutation caches.
function isChunkErrorMessage(message: unknown): boolean {
  if (typeof message !== 'string') return false
  const lower = message.toLowerCase()
  return (
    lower.includes('dynamically imported module') ||
    lower.includes('importing a module script failed') ||
    lower.includes('failed to load module script') ||
    lower.includes('expected a javascript module script') ||
    lower.includes('loading chunk') ||
    lower.includes('loading css chunk')
  )
}

// ─── Scrubbing (client-side twin of internal/errtrack/scrub.go) ─────────────

// Same substring list as the Go scrubber. A key whose lowercased name
// contains any of these gets its value replaced wholesale.
const SENSITIVE_KEY_PARTS = [
  'authorization',
  'cookie',
  'token',
  'password',
  'secret',
  'api_key',
  'apikey',
  'private_key',
  'credential',
  'session',
]

export const REDACTED = '[REDACTED]'

function isSensitiveKey(key: string): boolean {
  const k = key.toLowerCase()
  return SENSITIVE_KEY_PARTS.some((part) => k.includes(part))
}

/** Recursively redact values under credential-shaped keys. Mutates and returns obj. */
export function redactSensitive<T extends Record<string, unknown>>(obj: T): T {
  for (const [key, value] of Object.entries(obj)) {
    if (isSensitiveKey(key)) {
      ;(obj as Record<string, unknown>)[key] = REDACTED
    } else if (value && typeof value === 'object' && !Array.isArray(value)) {
      redactSensitive(value as Record<string, unknown>)
    }
  }
  return obj
}

// sanitizeContext deep-copies caller-supplied context before redaction so a
// caller passing a live object (a query-cached entity, say) never gets
// [REDACTED] written back into app state, then redacts on the copy. Depth-
// and cycle-limited so a pathological context can't recurse forever; anything
// non-plain is stringified. Exported for tests.
export function sanitizeContext(ctx: Record<string, unknown>): Record<string, unknown> {
  return redactSensitive(safeClone(ctx, 4, new WeakSet()) as Record<string, unknown>)
}

function safeClone(value: unknown, depth: number, seen: WeakSet<object>): unknown {
  if (value === null || typeof value !== 'object') {
    return typeof value === 'function' ? String(value) : value
  }
  if (depth <= 0 || seen.has(value)) return '[Truncated]'
  seen.add(value)
  if (Array.isArray(value)) {
    return value.map((v) => safeClone(v, depth - 1, seen))
  }
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(value)) {
    out[k] = safeClone(v, depth - 1, seen)
  }
  return out
}

// Path prefixes whose next segment is a live credential: the invite,
// password-reset and chart-share links, and the public invite lookup API.
// Twin: tokenPathPrefixes in internal/errtrack/scrub.go. Guard tests keep both
// lists in step with the token routes, and both scrubbers run
// internal/errtrack/testdata/redaction_cases.json.
export const TOKEN_PATH_PREFIXES = ['invite', 'invites/lookup', 'reset-password', 'shared/charts']

// One prefix character, literal or percent-encoded in either letter case: the
// router decodes /%69nvite/ to /invite/, while a recorded URL keeps the encoding.
function prefixChar(c: string): string {
  const encode = (ch: string) => '%' + ch.charCodeAt(0).toString(16).toUpperCase().padStart(2, '0')
  const alternatives = new Set([c.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&'), encode(c.toLowerCase()), encode(c.toUpperCase())])
  return `(?:${[...alternatives].join('|')})`
}

// A prefix and its token segment; group 1 is the prefix. Case-insensitive,
// because React Router matches routes that way.
const TOKEN_PATH_RE = new RegExp(
  `(\\/(?:${TOKEN_PATH_PREFIXES.map((p) => [...p].map(prefixChar).join('')).join('|')})\\/)[^/?#\\s"']+`,
  'gi',
)

/**
 * Replace the token segment of every token-bearing path in `s` with REDACTED,
 * keeping everything else, query included. `s` may be a full URL, a relative
 * path, or free text with several of either.
 */
export function redactTokenPaths(s: string): string {
  return s.replace(TOKEN_PATH_RE, `$1${REDACTED}`)
}

/**
 * Strip query, fragment and userinfo from a URL, and redact its token segment.
 * ?source_text= carries customer segment text (the terminology lookup in
 * api/terminology.ts) — a proven leak path — and /invite/{token} carries a
 * live credential. Falls back to a plain cut when the URL doesn't parse, so a
 * malformed or relative URL can't leak a query through the error path.
 */
export function stripUrl(raw: string): string {
  if (!raw) return raw
  try {
    const u = new URL(raw)
    u.search = ''
    u.hash = ''
    u.username = ''
    u.password = ''
    return redactTokenPaths(u.toString())
  } catch {
    const i = raw.search(/[?#]/)
    return redactTokenPaths(i >= 0 ? raw.slice(0, i) : raw)
  }
}

// Token shapes that can end up interpolated into error messages: JWTs (the
// localStorage auth token) and long hex/base64 runs (API keys, digests).
const JWT_RE = /eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*/g
const LONG_HEX_RE = /\b[A-Fa-f0-9]{32,}\b/g
// URLs inside messages lose their query/fragment the same way notice.url does.
// A quote ends the URL, so a quoted URL keeps its closing quote.
const URL_QUERY_RE = /(https?:\/\/[^\s?#"']+)[?#][^\s"']*/g

export function scrubMessage(message: string): string {
  return redactTokenPaths(message.replace(URL_QUERY_RE, '$1'))
    .replace(JWT_RE, REDACTED)
    .replace(LONG_HEX_RE, REDACTED)
}

// The subset of the Honeybadger notice we touch. Kept structural (not the
// SDK's Notice type) so the scrubber is trivially unit-testable.
export interface ScrubbableNotice {
  url?: string
  message?: string
  context?: Record<string, unknown>
  params?: Record<string, unknown>
  headers?: Record<string, unknown>
  cgiData?: Record<string, unknown>
}

/**
 * beforeNotify #1. Always returns true — scrubbing never drops a notice
 * (same contract as the Go scrubNotice). The backend proxy re-applies these
 * rules, so a bug here degrades defense-in-depth, not the boundary itself.
 */
export function scrubNotice(notice?: ScrubbableNotice): boolean {
  if (!notice) return true
  try {
    if (typeof notice.url === 'string') notice.url = stripUrl(notice.url)
    if (typeof notice.message === 'string') notice.message = scrubMessage(notice.message)
    for (const field of ['context', 'params', 'headers', 'cgiData'] as const) {
      const m = notice[field]
      if (m && typeof m === 'object') redactSensitive(m)
    }
  } catch {
    // Never let scrubbing itself break reporting.
  }
  return true
}

// ─── Throttling (client-side twin of the backend's per-fingerprint bucket) ──

const THROTTLE_LIMIT = 5 // per key per window — the same budget as the backend
const THROTTLE_WINDOW_MS = 60_000
// A runaway page could mint unbounded keys; reset the map past this size,
// mirroring maxTrackedFingerprints in the Go limiter.
const MAX_TRACKED_KEYS = 200

let buckets = new Map<string, { tokens: number; last: number }>()

/**
 * beforeNotify #2. Token bucket per error type (class + message prefix):
 * returning false drops the notice. An error loop (render crash → retry →
 * crash) must not burn the shared Honeybadger quota.
 *
 * Notices from our own notify()/notifyThen() already consumed a token before
 * reaching the SDK (throttlePrepaid) — this handler throttles only the SDK's
 * auto-caught path (window.onerror / unhandledrejection).
 */
export function throttleNotice(notice?: { name?: string; message?: string }, now = Date.now()): boolean {
  if (throttlePrepaid) return true
  const key = `${notice?.name ?? ''}|${(notice?.message ?? '').slice(0, 100)}`
  if (buckets.size >= MAX_TRACKED_KEYS && !buckets.has(key)) {
    buckets = new Map()
  }
  const bucket = buckets.get(key)
  if (!bucket) {
    buckets.set(key, { tokens: THROTTLE_LIMIT - 1, last: now })
    return true
  }
  const refill = ((now - bucket.last) / THROTTLE_WINDOW_MS) * THROTTLE_LIMIT
  bucket.tokens = Math.min(THROTTLE_LIMIT, bucket.tokens + refill)
  bucket.last = now
  if (bucket.tokens < 1) return false
  bucket.tokens -= 1
  return true
}

/** Test hook: reset throttle state between cases. */
export function resetThrottle(): void {
  buckets = new Map()
}

// ─── Identity (advisory — the server has the last word) ─────────────────────
//
// Whatever this sets, applyServerIdentity in internal/api/errtrack_proxy.go
// deletes and rewrites from the request's session cookie before the notice
// leaves for Honeybadger — including user_email and, on an impersonated
// session, impersonating_user_email. The route is public,
// so a browser-asserted identity is one anyone could forge. What this sets
// stays local to the SDK's own context.

export function setIdentity(u: {
  id: number
  tenant_id?: number
  role?: string
  impersonating?: boolean
}): void {
  try {
    Honeybadger.setContext({
      // user_id gets special aggregation treatment in Honeybadger's UI.
      user_id: String(u.id),
      ...(u.tenant_id != null ? { tenant_id: String(u.tenant_id) } : {}),
      ...(u.role ? { role: u.role } : {}),
      // Operator-generated errors shouldn't read as tenant errors.
      ...(u.impersonating != null ? { impersonating: u.impersonating } : {}),
    })
  } catch {
    /* never break auth flow over telemetry */
  }
}

export function clearIdentity(): void {
  try {
    Honeybadger.clear()
  } catch {
    /* ditto */
  }
}
