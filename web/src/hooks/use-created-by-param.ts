// URL-backed "created by" filter state for the client-side asset lists
// (TMs, term bases).
//
// Those lists are fetched whole (a handful of rows per tenant) and filtered in
// memory — there's no paginated server query to feed, unlike the projects list
// which sends `created_by` to the backend. Keeping the value in the URL anyway
// means the filter survives refresh and is shareable, matching the projects and
// jobs filter bars.

import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

export function useCreatedByParam(): [number | null, (id: number | null) => void] {
  const [params, setParams] = useSearchParams()

  const raw = params.get('created_by')
  const parsed = raw != null ? Number(raw) : NaN
  const value = Number.isInteger(parsed) && parsed > 0 ? parsed : null

  const setValue = useCallback((id: number | null) => {
    setParams(prev => {
      const next = new URLSearchParams(prev)
      if (id == null) next.delete('created_by')
      else next.set('created_by', String(id))
      return next
    }, { replace: true })
  }, [setParams])

  return [value, setValue]
}
