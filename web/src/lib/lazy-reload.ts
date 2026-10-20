import { lazy, type ComponentType } from 'react'
import { notifyThen } from '@/lib/errtrack'

// ─── Stale-chunk recovery for lazy routes ──────────────────────────────────
//
// Our heavy routes (CATEditor + its vendor-tiptap chunk, DebugPage) are code-
// split with `React.lazy(() => import(...))`. The chunk filenames are content-
// hashed, so every deploy produces new names. A browser that loaded the app
// BEFORE a deploy still holds the old index.html referencing the old hashes;
// when the user then navigates into the editor, the dynamic import() requests a
// chunk that no longer exists on the freshly-deployed server. The import
// rejects ("Failed to fetch dynamically imported module") and, because Suspense
// does not catch rejected lazy imports, the failure bubbles to the CATEditor
// ErrorBoundary → the user sees "CATEditor crashed" mid-deploy.
//
// So on a chunk-load failure we force a ONE-TIME full page reload. Reloading
// re-fetches index.html and thus the new chunk hashes, and the import succeeds.
// The crash happens at editor-OPEN time (before any edit exists), so an
// automatic reload here loses no user work.
//
// A sessionStorage flag guards against a reload loop: if the reload happens and
// the import STILL fails (a genuine broken build, not a stale chunk), we give up
// and let the error propagate to the ErrorBoundary so the real error is shown.

// Substrings that identify a failed dynamic import across browsers. Kept broad
// on purpose — a false positive costs one reload; a false negative shows the
// scary crash box during every deploy.
const CHUNK_ERROR_SIGNATURES = [
  'failed to fetch dynamically imported module', // Chromium
  'error loading dynamically imported module', // Firefox
  'importing a module script failed', // Safari
  'failed to load module script', // MIME mismatch (server returned index.html for a .js)
  'expected a javascript module script', // MIME mismatch (alt wording)
  'loading chunk', // webpack-style "Loading chunk N failed"
  'loading css chunk',
]

export function isChunkLoadError(err: unknown): boolean {
  if (err == null) return false
  const name = typeof err === 'object' && 'name' in err ? String((err as { name: unknown }).name) : ''
  if (name === 'ChunkLoadError') return true
  const message =
    typeof err === 'object' && 'message' in err
      ? String((err as { message: unknown }).message)
      : typeof err === 'string'
        ? err
        : ''
  const lower = message.toLowerCase()
  return CHUNK_ERROR_SIGNATURES.some((sig) => lower.includes(sig))
}

const flagKey = (chunkKey: string) => `chunk-reload:${chunkKey}`

// Decides whether a failed lazy import should trigger a one-time reload. Pure +
// storage-injectable so it's unit-testable and safe under private-mode storage.
export function shouldReloadForChunkError(err: unknown, chunkKey: string, storage: Storage): boolean {
  if (!isChunkLoadError(err)) return false
  try {
    if (storage.getItem(flagKey(chunkKey))) {
      // Already reloaded once for this chunk this session and it's STILL failing
      // → real broken build, not a stale chunk. Clear the flag and give up so the
      // ErrorBoundary can show the genuine error.
      storage.removeItem(flagKey(chunkKey))
      return false
    }
    storage.setItem(flagKey(chunkKey), '1')
    return true
  } catch {
    // sessionStorage can throw (Safari private mode, disabled storage). Don't
    // risk an unguarded reload loop — just let the error propagate.
    return false
  }
}

// Report the stale chunk, then reload, so deploy-window failures stay visible.
// notifyThen reloads once delivery finishes or its timeout passes, so a hung
// notice can't block recovery and the reload can't cancel the in-flight
// notice. Exported for tests.
export function reportStaleChunkAndReload(err: unknown, chunkKey: string): void {
  notifyThen(
    err,
    { name: 'StaleChunkReload', fingerprint: `stale-chunk-${chunkKey}`, context: { chunkKey } },
    () => window.location.reload(),
  )
}

function getSessionStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.sessionStorage : null
  } catch {
    return null
  }
}

// Drop-in replacement for React.lazy that recovers from stale-chunk failures.
// `chunkKey` is a stable id used for the per-chunk reload guard.
// Same constraint React.lazy uses: `any` (not `unknown`) so components that
// declare props are accepted — parameters are contravariant.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function lazyWithReload<T extends ComponentType<any>>(
  factory: () => Promise<{ default: T }>,
  chunkKey: string,
) {
  return lazy(() =>
    factory()
      .then((mod) => {
        // Import succeeded — clear any stale guard flag from a prior deploy so a
        // future deploy gets a fresh one-reload budget.
        const storage = getSessionStorage()
        try {
          storage?.removeItem(flagKey(chunkKey))
        } catch {
          /* ignore */
        }
        return mod
      })
      .catch((err) => {
        const storage = getSessionStorage()
        if (storage && shouldReloadForChunkError(err, chunkKey, storage)) {
          reportStaleChunkAndReload(err, chunkKey)
          // Return a never-resolving promise so nothing flashes before the
          // reload navigates away.
          return new Promise<{ default: T }>(() => {})
        }
        throw err
      }),
  )
}
