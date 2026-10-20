import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useDebounce } from './useDebounce'

/**
 * A text input backed by one URL query param, written `delay` ms after the last
 * keystroke — the search box on a filtered list. Returns `[value, setValue]`
 * for the input; the URL param is the source of truth the list query reads.
 * An empty value removes the param. Other params are left alone.
 *
 * The write runs only when the debounced input changes. `setParams` gets a new
 * identity on every URL change, so watching it would re-write the old input
 * over a URL just changed from outside (Clear filters, back/forward). It also
 * writes only when the value differs from the URL: `setParams` navigates even
 * when it returns `prev` unchanged, and `prev` can be a stale snapshot, so a
 * no-op write on mount pulls back a page that another component (a guided
 * tour) just left.
 */
export function useDebouncedUrlParam(key: string, delay = 300) {
  const [params, setParams] = useSearchParams()
  const urlValue = params.get(key) ?? ''
  const [value, setValue] = useState(urlValue)
  const debounced = useDebounce(value, delay)

  // Read, not watched: the write effect answers the input, the sync effect
  // answers the URL. Declared first so it runs before both in each commit.
  const latest = useRef({ urlValue, setParams })
  useEffect(() => {
    latest.current = { urlValue, setParams }
  })

  useEffect(() => {
    const { urlValue, setParams } = latest.current
    if (debounced === urlValue) return
    setParams(prev => {
      const next = new URLSearchParams(prev)
      if (debounced) next.set(key, debounced)
      else next.delete(key)
      return next
    }, { replace: true })
  }, [debounced, key])

  // Follow the URL when the param changes from outside (Clear filters,
  // back/forward).
  useEffect(() => {
    setValue(urlValue)
  }, [urlValue])

  return [value, setValue] as const
}
