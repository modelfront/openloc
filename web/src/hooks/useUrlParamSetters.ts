import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

/**
 * Writers for URL-backed filters. `setParam` sets one query param, or removes
 * it when the value is empty — unless `keepEmpty` is set, for a filter whose
 * empty value differs from its absent default; `null` always removes it.
 * `clearParams` removes the given keys, or every param when called without
 * keys. Other params are kept, and history entries are replaced, not pushed.
 *
 * Make one call per change: react-router's `setParams` does not queue, so a
 * second call in the same tick starts from the same URL as the first and
 * overwrites it.
 */
export function useUrlParamSetters() {
  const [, setParams] = useSearchParams()

  const setParam = useCallback((key: string, value: string | null, opts?: { keepEmpty?: boolean }) => {
    setParams(prev => {
      const next = new URLSearchParams(prev)
      if (value == null || (value === '' && !opts?.keepEmpty)) next.delete(key)
      else next.set(key, value)
      return next
    }, { replace: true })
  }, [setParams])

  const clearParams = useCallback((keys?: readonly string[]) => {
    setParams(prev => {
      if (!keys) return new URLSearchParams()
      const next = new URLSearchParams(prev)
      for (const k of keys) next.delete(k)
      return next
    }, { replace: true })
  }, [setParams])

  return { setParam, clearParams }
}
