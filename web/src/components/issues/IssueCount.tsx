import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { countIssues, type IssueFilter, type IssueScope } from '@/api'
import { formatCount } from '@/lib/duration'
import { issuesKey } from '@/lib/query-keys'

/**
 * How many issues the list shows with its filters, over every page. The order
 * does not change the count, so a new sort fetches nothing; while a new filter
 * loads, the previous count stays.
 */
export function IssueCount({ scope, filter }: { scope: IssueScope; filter: IssueFilter }) {
  const { sort: _sort, ...filters } = filter
  const { data } = useQuery({
    queryKey: [...issuesKey(scope), 'count', filters],
    queryFn: () => countIssues(scope, filters),
    placeholderData: keepPreviousData,
  })
  if (data === undefined) return null
  return (
    <span className="text-sm text-muted-foreground" data-testid="issues-count">
      {formatCount(data)} issue{data === 1 ? '' : 's'}
    </span>
  )
}
