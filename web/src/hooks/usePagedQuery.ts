import { useCallback, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { keepPreviousData, useQuery, type QueryKey } from '@tanstack/react-query'
import { useUrlParamSetters } from './useUrlParamSetters'

/** Rows per page of a numbered list. */
const PAGE_SIZE = 10

/** One page of a list the server splits into pages. `total` is left out on a page past the end. */
export interface ListPage<T> {
  data: T[]
  total?: number
}

/**
 * One page of a numbered list whose page number sits in the URL (`?<pageKey>=`,
 * absent for page 1). The rows of the page before stay on screen while the
 * next page or a new filter loads. A page past the end (an old link, or the
 * last row on it removed) finds no row, so the list goes back to page 1.
 * `goToPage` leaves the scroll where it is: every page is about as tall, so
 * the page numbers stay under the pointer for the next click.
 */
export function usePagedQuery<T>({ pageKey, queryKey, queryFn, pageSize = PAGE_SIZE }: {
  pageKey: string
  /** The cache key of one page; it carries the filters and the page number. */
  queryKey: (page: number) => QueryKey
  queryFn: (offset: number, limit: number) => Promise<ListPage<T>>
  pageSize?: number
}) {
  const [params] = useSearchParams()
  const page = Math.max(1, Number.parseInt(params.get(pageKey) ?? '', 10) || 1)
  const { setParam } = useUrlParamSetters()
  const query = useQuery({
    queryKey: queryKey(page),
    queryFn: () => queryFn((page - 1) * pageSize, pageSize),
    placeholderData: keepPreviousData,
  })
  const rows = query.data?.data ?? []
  const total = query.data?.total
  const pageCount = total != null ? Math.ceil(total / pageSize) : page

  const pastEnd = page > 1 && query.data != null && !query.isPlaceholderData && rows.length === 0
  useEffect(() => {
    if (pastEnd) setParam(pageKey, null)
  }, [pastEnd, pageKey, setParam])

  const goToPage = useCallback(
    (next: number) => setParam(pageKey, next === 1 ? null : String(next)),
    [pageKey, setParam],
  )
  return { query, rows, total, page, pageCount, goToPage }
}

export type PagedQuery<T> = ReturnType<typeof usePagedQuery<T>>
