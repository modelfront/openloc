// CustomerLabel — read-only display of a project's customer link. Used by
// CustomerSelect's stale-FK indicator, ProjectDetail's read-only path for
// non-admins, and the ProjectList customer column.
//
// Renders one of three states based on `id` + `customers.all` cache:
//   - id is null/undefined         → italic muted "None"
//   - id matches a live customer   → the customer's name
//   - id set but no match in cache → italic rose "(deleted)" + tooltip
//
// Why a component (not a hook returning name + isDeleted):
//   The deleted state needs distinct visual treatment in every consumer.
//   Encapsulating the JSX prevents drift between call sites — a single source
//   of truth for "how does a customer name render across the app." Future
//   tweaks (icon, badge color) land in one place.

import { useQuery } from '@tanstack/react-query'
import { listCustomers } from '../../api'
import { queryKeys } from '../../lib/query-keys'

interface Props {
  id: number | null | undefined
  className?: string
  /** Override the "None" placeholder. E.g. dash for table cells. */
  emptyText?: string
}

export function CustomerLabel({ id, className = '', emptyText = 'None' }: Props) {
  const { data: customers } = useQuery({
    queryKey: queryKeys.customers.all,
    queryFn: listCustomers,
  })

  if (id == null) {
    return <span className={`italic text-gray-400 dark:text-gray-500 ${className}`}>{emptyText}</span>
  }

  // While customers is loading, show a quiet placeholder rather than flash
  // to "(deleted)" then back to the name.
  if (customers === undefined) {
    return <span className={`text-gray-400 ${className}`}>…</span>
  }

  const found = customers.find(c => c.id === id)
  if (!found) {
    return (
      <span
        className={`italic text-rose-600 dark:text-rose-400 ${className}`}
        title="Customer was deleted. Re-pick or clear on the project detail page."
        data-testid={`customer-label-deleted-${id}`}
      >
        (deleted)
      </span>
    )
  }
  return <span className={`text-gray-700 dark:text-gray-300 ${className}`} data-testid={`customer-label-${id}`}>{found.name}</span>
}
