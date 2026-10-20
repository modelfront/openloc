import '@testing-library/jest-dom/vitest'

// ─── Web Storage polyfill ─────────────────────────────────────────────────
//
// Node ≥ 22 defines `localStorage` / `sessionStorage` as own globals that
// evaluate to `undefined` unless the process was started with
// `--localstorage-file`. Those own properties win over the ones jsdom would
// install, on `window` as well as on `globalThis`, so `localStorage.getItem`
// throws "Cannot read properties of undefined" in every test that touches
// storage — the app code is fine, the environment simply has no Storage.
//
// An in-memory Storage per test file is the right scope: a module registry is
// per file, so files can't leak stored state into each other, and the
// `localStorage.clear()` calls tests already make in `beforeEach` keep working.
//
// Guarded on `undefined` — a Node without this behaviour, or one started with
// the flag, keeps its own implementation.
function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() { return map.size },
    key: (i: number) => [...map.keys()][i] ?? null,
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => { map.set(k, String(v)) },
    removeItem: (k: string) => { map.delete(k) },
    clear: () => { map.clear() },
  } as Storage
}

for (const key of ['localStorage', 'sessionStorage'] as const) {
  if (globalThis[key] !== undefined) continue
  const impl = memoryStorage()
  Object.defineProperty(globalThis, key, { value: impl, configurable: true, writable: true })
  if (typeof window !== 'undefined' && window[key] === undefined) {
    Object.defineProperty(window, key, { value: impl, configurable: true, writable: true })
  }
}

// ─── jsdom polyfills for ProseMirror / TipTap ──────────────────────────────
//
// jsdom doesn't implement layout APIs (getClientRects, getBoundingClientRect)
// on text nodes — these are non-standard extensions that real browsers added.
// ProseMirror's `coordsAtPos` reaches into text nodes during selection scroll
// and crashes with "TypeError: target.getClientRects is not a function",
// sometimes after the test has completed, so afterEach teardown can't catch it.
//
// The polyfill returns zero-sized rects; no test asserts on layout values.
// Polyfill rather than filter the error by message.

const zeroRect: DOMRect = {
  x: 0,
  y: 0,
  width: 0,
  height: 0,
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
  toJSON() {
    return this
  },
}

const zeroRectList: DOMRectList = {
  length: 1,
  item: (i: number) => (i === 0 ? zeroRect : null),
  [0]: zeroRect,
  *[Symbol.iterator]() {
    yield zeroRect
  },
} as unknown as DOMRectList

function installLayoutPolyfill(target: { prototype: object } | undefined) {
  if (!target || !target.prototype) return
  if (!('getClientRects' in target.prototype)) {
    // @ts-expect-error — extending prototypes with non-standard layout methods
    target.prototype.getClientRects = function getClientRects() {
      return zeroRectList
    }
  }
  if (!('getBoundingClientRect' in target.prototype)) {
    // @ts-expect-error — extending prototypes with non-standard layout methods
    target.prototype.getBoundingClientRect = function getBoundingClientRect() {
      return zeroRect
    }
  }
}

// ProseMirror calls getClientRects on Text nodes, Range objects, and Elements
// during selection / scroll-into-view paths. Polyfill all three.
installLayoutPolyfill(typeof Text !== 'undefined' ? Text : undefined)
installLayoutPolyfill(typeof Range !== 'undefined' ? Range : undefined)
installLayoutPolyfill(typeof Element !== 'undefined' ? Element : undefined)

// ─── jsdom polyfill for cmdk / Radix (ResizeObserver + scrollIntoView) ────
// cmdk uses ResizeObserver internally for list virtualization and calls
// scrollIntoView on highlighted items. jsdom doesn't implement either.
// No-op stubs are fine since we don't assert on layout.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
}
if (typeof Element !== 'undefined' && !('scrollIntoView' in Element.prototype)) {
  // @ts-expect-error — extending prototype with non-standard method
  Element.prototype.scrollIntoView = function scrollIntoView() {}
}

// VirtualDataTable's infinite-scroll sentinel uses IntersectionObserver to
// detect when the bottom of the list is in view. jsdom doesn't ship it.
// No-op stub is fine: tests don't assert on infinite-scroll trigger behavior.
if (typeof globalThis.IntersectionObserver === 'undefined') {
  globalThis.IntersectionObserver = class IntersectionObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() { return [] }
    root = null
    rootMargin = ''
    thresholds = []
  } as unknown as typeof IntersectionObserver
}
