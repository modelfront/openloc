import { useMemo, useRef, type CSSProperties, type Dispatch, type ReactNode, type SetStateAction } from 'react'
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
import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-react'
import { Button } from './button'
import { ColumnResizeHandle } from './column-resize-handle'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './tooltip'
import { usePageRowVirtualizer } from './use-page-row-virtualizer'
import { tableHeaderRowClass } from '@/components/list/list-row'

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
   * The column is sortable: its header becomes a button that calls `onToggle`
   * with whether Shift was held. `direction` is how the list is sorted by this
   * column, or null when it is not: then the button shows a faint two-way
   * arrow. In a sort of several levels, `rank` (1, 2, …) is this column's
   * place, shown next to the arrow; only the first level carries `aria-sort`.
   * `hint` is the button's tooltip. `arrow` overrides the arrow `direction`
   * implies, for a list whose columns each start in a natural order: down for
   * that order, up for its reverse. The caller sorts the data (usually on the
   * server, since lists load in pages).
   */
  sort?: SortMeta
}

export interface SortMeta {
  direction: 'asc' | 'desc' | null
  rank?: number
  hint?: string
  arrow?: 'down' | 'up'
  onToggle: (shift: boolean) => void
}

const ORDINALS = ['1st', '2nd', '3rd']

/** A sortable column's header: the label, the arrow, and the level's rank. */
function SortButton({ sort, id, children }: { sort: SortMeta; id: string; children: ReactNode }) {
  const arrow = sort.direction && (sort.arrow ?? (sort.direction === 'desc' ? 'down' : 'up'))
  const Arrow = arrow === 'down' ? ArrowDown : arrow === 'up' ? ArrowUp : ArrowUpDown
  const button = (
    <Button variant="ghost" size="xs" data-testid={`sort-${id}`}
      className="-mx-2 font-medium uppercase tracking-wider text-inherit"
      // Shift+click must not select the header text.
      onMouseDown={e => { if (e.shiftKey) e.preventDefault() }}
      onClick={e => sort.onToggle(e.shiftKey)}>
      {children}
      <Arrow className={cn(!sort.direction && 'opacity-40')} aria-hidden />
      {sort.rank && sort.direction && (
        <span className="text-[10px] tabular-nums" data-testid={`sort-rank-${id}`}>
          <span aria-hidden>{sort.rank}</span>
          <span className="sr-only">
            sorted {ORDINALS[sort.rank - 1] ?? sort.rank}, {sort.direction === 'desc' ? 'descending' : 'ascending'}
          </span>
        </span>
      )}
    </Button>
  )
  if (!sort.hint) return button
  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger render={button} />
        <TooltipContent>{sort.hint}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/** aria-sort goes on one header: the column the list is first sorted by. */
function ariaSort(sort: SortMeta | undefined): 'ascending' | 'descending' | undefined {
  if (!sort?.direction || (sort.rank ?? 1) !== 1) return undefined
  return sort.direction === 'desc' ? 'descending' : 'ascending'
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
  /** Optional: row click handler. It gets the event too, for a click with a
   *  modifier key (see openRowLink). */
  onRowClick?: (row: TData, event: React.MouseEvent) => void
  /** Optional: a middle click on a row, outside its links and controls — for a
   *  row that opens a page, the new tab a link would open (see openRowLink). */
  onRowAuxClick?: (row: TData, event: React.MouseEvent) => void
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
  /**
   * Optional: mount only the rows near the screen, for a long table that
   * grows the page (it scrolls with its nearest scrolling ancestor, not in a
   * box of its own). Every row is `rowHeight` px tall, so the rows' content
   * must fit one line.
   */
  virtualRows?: { rowHeight: number }
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
  onRowAuxClick,
  rowClassName,
  rowTestId,
  meta,
  columnSizing,
  onColumnSizingChange,
  virtualRows,
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

  const rows = table.getRowModel().rows
  const virtualizer = usePageRowVirtualizer(boxRef, rows.length, virtualRows?.rowHeight)
  const items = virtualizer?.getVirtualItems()
  const padTop = items?.length ? items[0].start : 0
  const padBottom = items?.length ? virtualizer!.getTotalSize() - items[items.length - 1].end : 0
  const shown = items ? items.map(i => ({ row: rows[i.index], index: i.index })) : rows.map((row, index) => ({ row, index }))
  const spacer = (height: number) => height > 0 && (
    <tr aria-hidden style={{ height }}><td colSpan={columns.length} className="p-0" /></tr>
  )

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
              className={tableHeaderRowClass}
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
                    aria-sort={ariaSort(colMeta?.sort)}
                    onClick={cellClickHandler(onHeaderClick && (() => onHeaderClick(table)))}
                  >
                    {header.isPlaceholder ? null : colMeta?.sort ? (
                      <SortButton sort={colMeta.sort} id={id}>
                        {flexRender(header.column.columnDef.header, header.getContext())}
                      </SortButton>
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
          {rows.length === 0 ? (
            <tr>
              <td colSpan={columns.length} className="text-center py-8 text-sm text-gray-400 dark:text-gray-500">
                {emptyMessage}
              </td>
            </tr>
          ) : (<>
            {spacer(padTop)}
            {shown.map(({ row, index }) => {
              const rClassName = typeof rowClassName === 'function' ? rowClassName(row.original) : rowClassName
              return (
                <tr
                  key={row.id}
                  data-index={virtualizer ? index : undefined}
                  style={virtualRows && { height: virtualRows.rowHeight }}
                  className={cn(
                    'border-b border-gray-100 hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-gray-800',
                    onRowClick && 'cursor-pointer',
                    rClassName,
                  )}
                  onClick={onRowClick ? e => onRowClick(row.original, e) : undefined}
                  onAuxClick={onRowAuxClick ? e => {
                    if (e.button !== 1 || (e.target as HTMLElement).closest('a, button, input, label')) return
                    onRowAuxClick(row.original, e)
                  } : undefined}
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
            })}
            {spacer(padBottom)}
          </>)}
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
