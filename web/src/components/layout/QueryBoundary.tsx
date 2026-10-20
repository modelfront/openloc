import type { ReactNode } from 'react'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'

/** The fields of a `useQuery` result the boundary reads; a query result fits as is. */
export type QueryState<T> = { data: T | undefined; isPending: boolean; error: unknown; refetch: () => unknown }

/**
 * The body of a card that owns one query: `children(data)` once the data is
 * there, a small loading placeholder while the first fetch runs, and
 * `LoadFailed` with a Retry when it failed. Kept data wins over a failed
 * background refetch, so a card never swaps loaded content for an error.
 * `what` names the thing in the error ("Couldn't load the {what}: …").
 */
export function QueryBoundary<T>({ query, what, children }: {
  query: QueryState<T>
  what: string
  children: (data: T) => ReactNode
}) {
  if (query.data !== undefined) return <>{children(query.data)}</>
  if (query.isPending) return <LoadingPlaceholder size="sm" />
  return <LoadFailed what={what} error={query.error} onRetry={() => void query.refetch()} />
}
