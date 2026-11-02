import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

/**
 * A text input backed by one URL query param, written `delay` ms after the last
 * keystroke — the search box of a URL-backed list. Returns `[value, setValue]`
 * for the input. The input keeps its own state, so a list filtered in the
 * browser filters on `value` and narrows as you type; a server-backed list
 * reads the URL param. An empty value removes the param. Other params are left
 * alone.
 *
 * The router applies a URL change as a transition, so the URL can still show a
 * value this hook wrote while the user types on. `written` holds those values
 * until the URL reaches them: only a URL value that is not one of them is a
 * change from outside (Clear filters, back/forward) and replaces the input.
 *
 * A write is skipped when the input already matches what the URL holds or is
 * about to hold: `setParams` navigates even when it returns `prev` unchanged,
 * and `prev` can be a stale snapshot, so a no-op write on mount pulls back a
 * page that another component (a guided tour) just left.
 */
export function useDebouncedUrlParam(key: string, delay = 300) {
  const [params, setParams] = useSearchParams()
  const urlValue = params.get(key) ?? ''
  const [value, setValue] = useState(urlValue)
  const [written, setWritten] = useState<string[]>([])
  const [seenUrl, setSeenUrl] = useState(urlValue)

  // The URL param changed since the last render: adjust state while rendering,
  // so the write effect below never sees the old input against the new URL.
  if (urlValue !== seenUrl) {
    setSeenUrl(urlValue)
    const own = written.indexOf(urlValue)
    if (own >= 0) {
      setWritten(written.slice(own + 1))
    } else {
      setWritten([])
      setValue(urlValue)
    }
  }

  const expected = written.at(-1) ?? urlValue
  useEffect(() => {
    if (value === expected) return
    const timer = setTimeout(() => {
      setWritten(w => [...w, value])
      setParams(prev => {
        const next = new URLSearchParams(prev)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      }, { replace: true })
    }, delay)
    return () => clearTimeout(timer)
  }, [value, expected, key, delay, setParams])

  return [value, setValue] as const
}
