import { useQueries, useQuery, type QueryClient, type UseQueryResult } from '@tanstack/react-query'
import { ApiError, getEditorIndex, getEditorRefs, getEditorRows, searchEditor, type EditorRow, type EditorSearch } from '../api'
import { queryKeys } from '../lib/query-keys'
import { indexLookup, toIndexData, type IndexRow } from '../lib/editor-index'

const EMPTY_LOOKUP = indexLookup([])

/** Rows per loaded page. A page is a slice of the index, fetched by segment id. */
export const EDITOR_PAGE_SIZE = 50

/** How long a loaded page counts as fresh: scrolling back to it within this
 *  time reuses it instead of fetching it again. Saves patch it in place, and a
 *  job-wide change invalidates it. */
const PAGE_STALE_MS = 30_000

/**
 * The CAT editor's index of every segment: loaded once, small, and patched by
 * every save. Counts, filters, selection and navigation work over it, so they
 * cover the whole file while only the rows on screen carry their text.
 */
export function useEditorIndex(jobId: number, enabled: boolean) {
  const query = useQuery({
    queryKey: queryKeys.job.editorIndex(jobId),
    queryFn: async () => toIndexData(await getEditorIndex(jobId)),
    enabled,
  })
  return {
    rows: query.data?.rows,
    lookup: query.data?.lookup ?? EMPTY_LOOKUP,
    repetitionStats: query.data?.repetitionStats,
    isPending: query.isPending,
    // Only a failed first load: a failed refetch keeps the rows already shown.
    error: query.isLoadingError ? query.error : null,
    refetch: query.refetch,
  }
}

/** The pages that hold these index positions. */
export function pagesFor(indices: Iterable<number>): number[] {
  const pages = new Set<number>()
  for (const i of indices) if (i >= 0) pages.add(Math.floor(i / EDITOR_PAGE_SIZE))
  return [...pages].sort((a, b) => a - b)
}

/** A page's cache key part: the segments it holds, in order. A split or join
 *  changes them without moving the page's first row, and the page must then
 *  be fetched again with the new ids. */
export function editorPageKey(rows: readonly Pick<IndexRow, 'segmentId'>[]): string {
  return rows.map(r => r.segmentId).join(',')
}

function combineRows(results: UseQueryResult<EditorRow[]>[]): Map<number, EditorRow> {
  const bySegment = new Map<number, EditorRow>()
  for (const r of results) for (const row of r.data ?? []) bySegment.set(row.segment.id, row)
  return bySegment
}

/**
 * Text of the rows at these index positions, by segment id. Each page is its
 * own query: pages leave the cache a while after they scroll out of view, and
 * a job-wide invalidation refetches only the pages still on screen.
 */
export function useEditorPages(jobId: number, rows: readonly IndexRow[] | undefined, indices: Iterable<number>) {
  const pages = rows ? pagesFor(indices) : []
  return useQueries({
    queries: pages.map(page => editorPageQuery(jobId, rows!, page)),
    combine: combineRows,
  })
}

/** The query of one page of rows of this index. */
function editorPageQuery(jobId: number, rows: readonly IndexRow[], page: number) {
  const slice = rows.slice(page * EDITOR_PAGE_SIZE, (page + 1) * EDITOR_PAGE_SIZE)
  return {
    queryKey: queryKeys.job.editorPage(jobId, page, editorPageKey(slice)),
    queryFn: () => getEditorRows(jobId, slice.map(r => r.segmentId)),
    enabled: slice.length > 0,
    staleTime: PAGE_STALE_MS,
  }
}

/**
 * Fetches the job's index again, and the pages on screen as the new index
 * cuts them, then puts the index in the cache: a split or join changes the
 * segment ids a page holds, and the rows on screen change in one step instead
 * of showing as loading until their page arrives. The page after the last one
 * on screen comes too, because a split moves the rows below it down by one.
 * Every page is fetched however fresh its cached copy: a split joined back
 * gives a page its old ids again, and the copy under them predates both. A
 * page that fails to load rejects the whole refresh, so the caller does not
 * trust the cached rows.
 */
export async function refetchIndexWithPages(queryClient: QueryClient, jobId: number): Promise<void> {
  const data = toIndexData(await getEditorIndex(jobId))
  const shown = queryClient.getQueryCache()
    .findAll({ queryKey: queryKeys.job.editorRows(jobId), type: 'active' })
    .map(q => q.queryKey[3] as number)
  const pages = shown.length ? new Set([...shown, Math.max(...shown) + 1]) : new Set<number>()
  await Promise.all([...pages].map(page => {
    const query = editorPageQuery(jobId, data.rows, page)
    return query.enabled ? queryClient.fetchQuery({ ...query, staleTime: 0 }) : undefined
  }))
  queryClient.setQueryData(queryKeys.job.editorIndex(jobId), data)
}

/** Where the server text search stands; null when it has answered or there is no search. */
export type EditorSearchState = 'pending' | 'failed' | 'invalid' | null

/**
 * The filter bar's text search, answered by the server over the whole file.
 * `matches` is null when there is no search text. Until the answer for the
 * current text arrives, and when the search fails, it is empty, so nothing the
 * search has not found is shown or selected; `state` says why. A pattern the
 * server refuses (RE2 has no lookahead, for one) is `invalid`.
 */
export function useEditorSearch(jobId: number, search: EditorSearch | null): { matches: ReadonlySet<number> | null; state: EditorSearchState } {
  const query = useQuery({
    queryKey: queryKeys.job.editorSearch(jobId, search ?? { text: '', source: true, target: true, caseSensitive: false, regex: false }),
    queryFn: async ({ signal }) => new Set(await searchEditor(jobId, search!, signal)),
    enabled: search !== null,
    retry: false,
  })
  if (search === null) return { matches: null, state: null }
  if (query.isError) {
    const refused = search.regex && query.error instanceof ApiError && query.error.status === 400
    return { matches: EMPTY, state: refused ? 'invalid' : 'failed' }
  }
  if (!query.data) return { matches: EMPTY, state: 'pending' }
  return { matches: query.data, state: null }
}

const EMPTY: ReadonlySet<number> = new Set()

/** The segments the file's cross-reference chips point at, by key. */
export function useEditorRefs(jobId: number, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.job.editorRefs(jobId),
    queryFn: () => getEditorRefs(jobId),
    enabled,
  })
}
