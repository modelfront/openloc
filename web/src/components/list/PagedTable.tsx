import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { EmptyState } from '@/components/layout/EmptyState'
import { DataTable, type ColumnDef } from '@/components/ui/data-table'
import { Pagination } from '@/components/ui/pagination'
import type { PagedQuery } from '@/hooks/usePagedQuery'

/**
 * The body of a `flush` SettingsCard that holds a numbered list
 * (`usePagedQuery`): a placeholder while the first page loads, the error with
 * Retry, the empty message, or the table wall to wall with the page numbers
 * under it. Everything but the table pads itself.
 */
export function PagedTable<T>({ paged, what, emptyMessage, columns, onRowClick, rowTestId, rowClassName, testId }: {
  paged: PagedQuery<T>
  /** What the list holds, for the error: "Couldn't load the <what>". */
  what: string
  emptyMessage: string
  /** Module scope or memoized: new column functions remount every cell. */
  columns: ColumnDef<T>[]
  onRowClick?: (row: T) => void
  rowTestId: (row: T) => string
  rowClassName?: string
  /** The page numbers' test id. */
  testId: string
}) {
  const { query, rows, page, pageCount, goToPage } = paged
  if (query.isPending) return <div className="p-3"><LoadingPlaceholder size="sm" /></div>
  if (query.isError && !query.data) {
    return <div className="p-3"><LoadFailed what={what} error={query.error} onRetry={() => void query.refetch()} /></div>
  }
  if (rows.length === 0) return <div className="p-3"><EmptyState message={emptyMessage} /></div>
  return (
    <>
      <DataTable
        columns={columns}
        data={rows}
        onRowClick={onRowClick}
        rowTestId={rowTestId}
        rowClassName={rowClassName}
      />
      <Pagination page={page} pageCount={pageCount} onPageChange={goToPage} testId={testId} />
    </>
  )
}
