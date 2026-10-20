/** The part of a TanStack query result that `loadFailure` reads. */
export interface FailableQuery {
  isError: boolean
  data: unknown
  error: unknown
  refetch: () => unknown
}

/**
 * Whether a view that needs all of `queries` should show `LoadFailed`, and
 * with what. Only a query with nothing to show counts: a failed background
 * refetch keeps the data already loaded on screen. Retry refetches every
 * failed query; the error shown is the first one's.
 */
export function loadFailure(...queries: FailableQuery[]): { error: unknown; retry: () => void } | null {
  const failed = queries.filter(q => q.isError && q.data === undefined)
  if (failed.length === 0) return null
  return {
    error: failed[0].error,
    retry: () => { for (const q of failed) void q.refetch() },
  }
}
