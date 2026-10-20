// Attention across pages. A link built with `attentionHref(path, key)` opens
// the page with `?attention=<key>`; the element there that declares the key
// with `useAttentionTarget(key)` is scrolled to and ringed (`drawAttention`)
// the moment it is on screen — after its data has loaded, however long that
// takes. The provider sits in the app shell: it reads the parameter on every
// location change, strips it from the URL (a reload or Back must not ring
// again) and keeps the key until a target claims it, the user moves to another
// page, or it goes stale. A target that never appears is a silent no-op.
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { ATTENTION_PARAM, drawAttention } from '@/lib/attention'

const PENDING_MS = 10_000

interface Registry {
  register: (key: string, el: HTMLElement) => () => void
}

const AttentionContext = createContext<Registry | null>(null)

export function AttentionProvider({ children }: { children: ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const targets = useRef(new Map<string, HTMLElement>())
  // The location as of the last commit, read by the deferred strip below.
  const latest = useRef(location)
  useLayoutEffect(() => { latest.current = location })
  const pending = useRef<{ key: string; pathname: string; expires: number } | null>(null)

  const tryClaim = useCallback(() => {
    const p = pending.current
    if (!p) return
    if (Date.now() > p.expires) {
      pending.current = null
      return
    }
    const el = targets.current.get(p.key)
    if (!el) return
    pending.current = null
    drawAttention({ target: el })
  }, [])

  const register = useCallback((key: string, el: HTMLElement) => {
    targets.current.set(key, el)
    tryClaim()
    return () => {
      if (targets.current.get(key) === el) targets.current.delete(key)
    }
  }, [tryClaim])

  useEffect(() => {
    const params = new URLSearchParams(location.search)
    const key = params.get(ATTENTION_PARAM)
    if (key) {
      pending.current = { key, pathname: location.pathname, expires: Date.now() + PENDING_MS }
      tryClaim()
      // Stripped a tick later, and only if nothing moved on meanwhile: a page
      // that redirects on mount navigates in this same commit, and replacing
      // with the location seen here would undo that redirect.
      params.delete(ATTENTION_PARAM)
      const search = params.toString()
      const seen = location.key
      const strip = window.setTimeout(() => {
        if (latest.current.key !== seen) return
        navigate({ pathname: location.pathname, search: search ? `?${search}` : '', hash: location.hash }, { replace: true })
      }, 0)
      return () => window.clearTimeout(strip)
    } else if (pending.current && pending.current.pathname !== location.pathname) {
      // Moving on to another page drops a key nothing claimed.
      pending.current = null
    }
  }, [location, navigate, tryClaim])

  const registry = useMemo(() => ({ register }), [register])
  return <AttentionContext.Provider value={registry}>{children}</AttentionContext.Provider>
}

/** Declares the element the callback ref is given as the target of `key`.
 *  Attach it as `ref` on the element a link may point at. Outside the
 *  provider (a test, a dialog rendered alone) it is inert. */
export function useAttentionTarget(key: string) {
  const registry = useContext(AttentionContext)
  const unregister = useRef<(() => void) | null>(null)
  return useCallback((el: HTMLElement | null) => {
    unregister.current?.()
    unregister.current = el && registry ? registry.register(key, el) : null
  }, [registry, key])
}
