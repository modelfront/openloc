// @vitest-environment node
// Pure logic — no DOM needed. Runs under the node environment so it doesn't
// depend on jsdom (and stays green regardless of jsdom's transitive-dep state).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// Mock errtrack: the real module pulls in the browser Honeybadger SDK, which
// needs a window; the reload test asserts it sends no notice.
vi.mock('@/lib/errtrack', () => ({ notify: vi.fn() }))
import { notify } from '@/lib/errtrack'
import { isChunkLoadError, shouldReloadForChunkError, reloadForStaleChunk } from './lazy-reload'

describe('isChunkLoadError', () => {
  it('matches the Chromium "Failed to fetch dynamically imported module" message', () => {
    expect(
      isChunkLoadError(new TypeError('Failed to fetch dynamically imported module: https://app/assets/CATEditor-a1b2c3.js')),
    ).toBe(true)
  })

  it('matches the Firefox "error loading dynamically imported module" message', () => {
    expect(isChunkLoadError(new TypeError('error loading dynamically imported module'))).toBe(true)
  })

  it('matches the Safari "Importing a module script failed" message', () => {
    expect(isChunkLoadError(new TypeError('Importing a module script failed.'))).toBe(true)
  })

  it('matches a MIME-type mismatch (server returned index.html for a .js request)', () => {
    expect(
      isChunkLoadError(
        new TypeError(
          "Failed to load module script: Expected a JavaScript module script but the server responded with a MIME type of \"text/html\".",
        ),
      ),
    ).toBe(true)
  })

  it('matches a webpack-style ChunkLoadError by name', () => {
    const err = new Error('Loading chunk 5 failed')
    err.name = 'ChunkLoadError'
    expect(isChunkLoadError(err)).toBe(true)
  })

  it('does NOT match a genuine runtime error thrown from inside the component', () => {
    expect(isChunkLoadError(new TypeError("Cannot read properties of undefined (reading 'map')"))).toBe(false)
    expect(isChunkLoadError(new Error('boom'))).toBe(false)
    expect(isChunkLoadError('not an error')).toBe(false)
    expect(isChunkLoadError(null)).toBe(false)
  })
})

describe('shouldReloadForChunkError', () => {
  let store: Record<string, string>
  const storage = {
    getItem: (k: string) => (k in store ? store[k] : null),
    setItem: (k: string, v: string) => {
      store[k] = v
    },
    removeItem: (k: string) => {
      delete store[k]
    },
  } as unknown as Storage

  beforeEach(() => {
    store = {}
  })

  it('reloads once for a chunk error, then refuses on the second attempt (loop guard)', () => {
    const err = new TypeError('Failed to fetch dynamically imported module')
    // First failure this session → reload.
    expect(shouldReloadForChunkError(err, 'CATEditor', storage)).toBe(true)
    // A flag was persisted so a reload loop can't form.
    expect(shouldReloadForChunkError(err, 'CATEditor', storage)).toBe(false)
  })

  it('never reloads for a non-chunk error, even the first time', () => {
    expect(shouldReloadForChunkError(new Error('render blew up'), 'CATEditor', storage)).toBe(false)
    // ...and doesn't burn the one-reload budget for a later genuine stale chunk.
    expect(shouldReloadForChunkError(new TypeError('Failed to fetch dynamically imported module'), 'CATEditor', storage)).toBe(true)
  })

  it('guards each chunk key independently', () => {
    const err = new TypeError('Failed to fetch dynamically imported module')
    expect(shouldReloadForChunkError(err, 'CATEditor', storage)).toBe(true)
    // A different lazy chunk gets its own one-reload budget.
    expect(shouldReloadForChunkError(err, 'DebugPage', storage)).toBe(true)
    expect(shouldReloadForChunkError(err, 'CATEditor', storage)).toBe(false)
  })

  it('tolerates a throwing storage (private mode) by not attempting a reload', () => {
    const brokenStorage = {
      getItem: () => {
        throw new Error('SecurityError')
      },
      setItem: () => {
        throw new Error('SecurityError')
      },
      removeItem: () => {},
    } as unknown as Storage
    expect(
      shouldReloadForChunkError(new TypeError('Failed to fetch dynamically imported module'), 'CATEditor', brokenStorage),
    ).toBe(false)
  })
})

describe('reloadForStaleChunk', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('reloads at once and sends no notice: a stale chunk is expected in every deploy window', () => {
    const reload = vi.fn()
    vi.stubGlobal('window', { location: { reload } })
    reloadForStaleChunk()
    expect(reload).toHaveBeenCalledTimes(1)
    expect(notify).not.toHaveBeenCalled()
  })
})
