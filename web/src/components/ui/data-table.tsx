import { useMemo, useRef, type CSSProperties, type Dispatch, type SetStateAction } from 'react'
import { flushSync } from 'react-dom'
import {
  type ColumnDef,
  type ColumnSizingState,
  type Table,
  type Updater,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'

import { cn } from '@/lib/utils'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { Button } from './button'
import { ColumnResizeHandle } from './column-resize-handle'

/**
 * Optional `meta` on a column. `onCellClick` / `onHeaderClick` make the whole
 * cell the click target — a checkbox column, where aiming at the box itself is
 * fiddly. Such a click never reaches the row's `onRowClick`, and a click on a
 * control inside the cell is left to that control.
 */
export interface DataTableColumnMeta<TData> {
  onCellClick?: (row: TData, table: Table<TData>) => void
  onHeaderClick?: (table: Table<TData>) => void
  /** Classes for the column's header and body cells alike — e.g. a container
   *  query that drops a low-priority column when the table is narrow. */
  className?: string
  /**
   * The list is sorted by this column, in `direction`: the header becomes a
   * button with an arrow that calls `onToggle`, and carries `aria-sort`. The
   * caller sorts the data (usually on the server, since lists load in pages).
   */
  sort?: { direction: 'asc' | 'desc'; onToggle: () => void }
}

const cellClickHandler = (run: (() => void) | undefined) => run && ((e: React.MouseEvent) => {
  e.stopPropagation()
  if ((e.target as HTMLElement).closest('input, button, a, label')) return
  run()
})

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[]
  data: TData[]
  /** Optional: render when no data */
  emptyMessage?: string
  /** Optional: className for the wrapper */
  className?: string
  /** Optional: row click handler */
  onRowClick?: (row: TData) => void
  /** Optional: className per row (e.g., for hover group). Merged last, so a
   *  row that owns its background (and hover) overrides the default grey hover. */
  rowClassName?: string | ((row: TData) => string)
  /** Optional: `data-testid` per row. */
  rowTestId?: (row: TData) => string
  /**
   * Optional: values the cells read through `table.options.meta` — a
   * selection, a role flag. Pass what changes here and keep `columns` stable
   * (module scope or useMemo): flexRender mounts each cell function as a
   * component, so new column functions remount every cell on each render.
   */
  meta?: Record<string, unknown>
  /**
   * Optional: column widths the user dragged, by column id. With
   * `onColumnSizingChange`, a column that sets `enableResizing: true` gets a
   * drag strip on its header's right edge; double-click resets it.
   */
  columnSizing?: ColumnSizingState
  onColumnSizingChange?: Dispatch<SetStateAction<ColumnSizingState>>
}

/**
 * A column with no `size` and no dragged width shares what the others leave.
 * A drag never takes those below this, so the table still fits its box.
 */
const MIN_FLEX_WIDTH = 120

/**
 * The ids of the columns declared without a `size`. TanStack fills in a
 * default size on every column, so the declaration is the only place that
 * says a column is flexible.
 */
function flexibleColumnIds<TData, TValue>(columns: ColumnDef<TData, TValue>[]): Set<string> {
  const ids = columns.filter(c => c.size === undefined).map(c => c.id ?? ('accessorKey' in c ? String(c.accessorKey) : undefined))
  return new Set(ids.filter(id => id !== undefined))
}

/** A column's width under `sizing`, clamped as TanStack clamps it; undefined while flexible. */
function widthOf<TData>(table: Table<TData>, id: string, sizing: ColumnSizingState, flexible: Set<string>): number | undefined {
  const c = table.getColumn(id)
  if (!c) return undefined
  const { size = 0, minSize = 0, maxSize = Infinity } = c.columnDef
  const w = sizing[id] ?? (flexible.has(id) ? undefined : size)
  return w === undefined ? undefined : Math.min(maxSize, Math.max(minSize, w))
}

/** What the visible columns need: their widths, and the floor of each flexible one. */
function neededWidth<TData>(table: Table<TData>, sizing: ColumnSizingState, flexible: Set<string>): number {
  return table.getVisibleLeafColumns()
    .reduce((n, c) => n + (widthOf(table, c.id, sizing, flexible) ?? MIN_FLEX_WIDTH), 0)
}

/**
 * `next`, with the columns that grew cut back so the table fits `box` — down
 * to their width in `prev` at most. A change that makes the table no wider
 * than before goes through as is, so a table that no longer fits (the window
 * shrank) can still be narrowed.
 */
function fitToBox<TData>(
  table: Table<TData>, prev: ColumnSizingState, next: ColumnSizingState, flexible: Set<string>, box: number,
): ColumnSizingState {
  const needed = neededWidth(table, next, flexible)
  if (needed <= box || needed <= neededWidth(table, prev, flexible)) return next
  let excess = needed - box
  const fitted = { ...next }
  for (const id of Object.keys(next)) {
    const before = widthOf(table, id, prev, flexible)
    const after = widthOf(table, id, next, flexible)
    if (before === undefined || after === undefined || after <= before) continue
    const cut = Math.min(excess, after - before)
    fitted[id] = after - cut
    excess -= cut
  }
  return excess > 0 ? prev : fitted
}

export function DataTable<TData, TValue>({
  columns,
  data,
  emptyMessage = 'No data.',
  className = '',
  onRowClick,
  rowClassName,
  rowTestId,
  meta,
  columnSizing,
  onColumnSizingChange,
}: DataTableProps<TData, TValue>) {
  const boxRef = useRef<HTMLDivElement>(null)
  const sizing = columnSizing ?? {}
  const flexibleIds = useMemo(() => flexibleColumnIds(columns), [columns])
  // A drag stops where the table would get wider than its box. A box with no
  // layout (width 0) is not measured.
  const fitSizing = (updater: Updater<ColumnSizingState>) => onColumnSizingChange?.(prev => {
    const next = typeof updater === 'function' ? updater(prev) : updater
    const box = boxRef.current?.clientWidth ?? 0
    return box === 0 ? next : fitToBox(table, prev, next, flexibleIds, box)
  })
  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
    meta,
    // TanStack resizes every column by default; here a column opts in, and only
    // when the caller keeps the widths.
    defaultColumn: { enableResizing: false },
    enableColumnResizing: onColumnSizingChange !== undefined,
    ...(onColumnSizingChange && {
      state: { columnSizing: sizing },
      onColumnSizingChange: fitSizing,
      columnResizeMode: 'onChange' as const,
    }),
  })

  return (
    // On a phone, narrower than what its columns need, the table scrolls sideways
    // inside its box instead of squeezing the columns over each other.
    <div ref={boxRef} className={cn('w-full', onColumnSizingChange ? 'overflow-x-auto' : 'overflow-x-hidden max-md:overflow-x-auto', className)}>
      <table
        className="w-full table-fixed max-md:min-w-(--table-min-width)"
        style={{ '--table-min-width': `${neededWidth(table, sizing, flexibleIds)}px` } as CSSProperties}
      >
        <thead>
          {table.getHeaderGroups().map(headerGroup => (
            <tr
              key={headerGroup.id}
              className="bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider"
            >
              {headerGroup.headers.map(header => {
                const colMeta = header.column.columnDef.meta as DataTableColumnMeta<TData> | undefined
                const onHeaderClick = colMeta?.onHeaderClick
                const { id } = header.column
                const flexible = flexibleIds.has(id) && sizing[id] === undefined
                return (
                  <th
                    key={header.id}
                    className={cn(
                      'px-3 py-2 text-left font-medium',
                      onHeaderClick && 'cursor-pointer',
                      header.column.getCanResize() && 'relative',
                      colMeta?.className,
                    )}
                    style={{ width: flexible ? undefined : header.getSize() }}
                    aria-sort={colMeta?.sort ? (colMeta.sort.direction === 'desc' ? 'descending' : 'ascending') : undefined}
                    onClick={cellClickHandler(onHeaderClick && (() => onHeaderClick(table)))}
                  >
                    {header.isPlaceholder ? null : colMeta?.sort ? (
                      <Button variant="ghost" size="xs" onClick={colMeta.sort.onToggle} data-testid={`sort-${id}`}
                        className="-mx-2 font-medium uppercase tracking-wider text-inherit">
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {colMeta.sort.direction === 'desc' ? <ArrowDown /> : <ArrowUp />}
                      </Button>
                    ) : flexRender(header.column.columnDef.header, header.getContext())}
                    {header.column.getCanResize() && (
                      <ColumnResizeHandle
                        aria-label={`Resize ${id} column`}
                        active={header.column.getIsResizing()}
                        onMouseDown={e => {
                          e.preventDefault() // no text selection while dragging
                          // A flexible column has no width TanStack knows, so
                          // the drag would start from its 150px default. Give
                          // it the width it shows first, synchronously, so the
                          // handler below reads it.
                          const th = e.currentTarget.parentElement
                          if (flexible && th) {
                            const shown = Math.round(th.getBoundingClientRect().width)
                            flushSync(() => onColumnSizingChange?.(s => ({ ...s, [id]: shown })))
                          }
                          header.getResizeHandler()(e)
                        }}
                        onClick={e => e.stopPropagation()}
                        onDoubleClick={e => { e.stopPropagation(); header.column.resetSize() }}
                      />
                    )}
                  </th>
                )
              })}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.length === 0 ? (
            <tr>
              <td colSpan={columns.length} className="text-center py-8 text-sm text-gray-400 dark:text-gray-500">
                {emptyMessage}
              </td>
            </tr>
          ) : (
            table.getRowModel().rows.map(row => {
              const rClassName = typeof rowClassName === 'function' ? rowClassName(row.original) : rowClassName
              return (
                <tr
                  key={row.id}
                  className={cn(
                    'border-b border-gray-100 hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-gray-800',
                    onRowClick && 'cursor-pointer',
                    rClassName,
                  )}
                  onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                  data-testid={rowTestId?.(row.original)}
                >
                  {row.getVisibleCells().map(cell => {
                    const colMeta = cell.column.columnDef.meta as DataTableColumnMeta<TData> | undefined
                    const onCellClick = colMeta?.onCellClick
                    return (
                      <td
                        key={cell.id}
                        className={`px-3 py-2 ${onCellClick ? 'cursor-pointer' : ''} ${colMeta?.className ?? ''}`}
                        onClick={cellClickHandler(onCellClick && (() => onCellClick(row.original, table)))}
                      >
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    )
                  })}
                </tr>
              )
            })
          )}
        </tbody>
      </table>
    </div>
  )
}

/**
 * VirtualDataTable — for large datasets with virtual scrolling.
 * Uses TanStack Table for column management + TanStack Virtual for row virtualization.
 */
export { type ColumnDef } from '@tanstack/react-table'
