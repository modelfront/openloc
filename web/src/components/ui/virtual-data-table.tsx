import { useRef, useEffect, type CSSProperties } from 'react'
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'

interface VirtualDataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[]
  data: TData[]
  estimateSize?: number
  onLoadMore?: () => void
  isLoadingMore?: boolean
  emptyMessage?: string
  onRowClick?: (row: TData) => void
  rowClassName?: string | ((row: TData) => string)
  className?: string
  // Opt-in: measure each row's real height instead of pinning it to
  // estimateSize. Needed for variable-height rows (e.g. a concept whose Terms
  // cell stacks one line per term). Cells also top-align in this mode.
  measureRows?: boolean
  // On a phone: narrowest a fill column (no explicit size) may get. Below the
  // sum of fixed widths and these, the table scrolls sideways instead of squeezing.
  minFillColumnWidth?: number
}

export function VirtualDataTable<TData, TValue>({
  columns,
  data,
  estimateSize = 40,
  onLoadMore,
  isLoadingMore,
  emptyMessage = 'No data.',
  onRowClick,
  rowClassName,
  className = '',
  measureRows = false,
  minFillColumnWidth = 160,
}: VirtualDataTableProps<TData, TValue>) {
  const scrollRef = useRef<HTMLDivElement>(null)

  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
  })

  const { rows } = table.getRowModel()

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => estimateSize,
    overscan: 10,
  })

  const virtualItems = virtualizer.getVirtualItems()
  const lastIndex = virtualItems.length ? virtualItems[virtualItems.length - 1].index : -1

  // Infinite scroll driven by the virtualizer's rendered range, NOT a
  // bottom-pinned IntersectionObserver sentinel. With measured rows whose real
  // height far exceeds `estimateSize`, an absolutely-positioned bottom:0 sentinel
  // sits at the WRONG place — as rows measure and the container grows, the true
  // bottom recedes below the viewport and the sentinel never intersects, so
  // pagination stalls (the count freezes at page 1). Firing when the last
  // rendered item is within a few rows of the end is height-independent and
  // robust. Depend on the last index (a primitive), NOT the
  // getVirtualItems() array, which is a new reference every render — otherwise
  // this effect re-runs on every unrelated re-render (e.g. each keystroke of an
  // inline edit).
  useEffect(() => {
    if (!onLoadMore || isLoadingMore) return
    if (lastIndex >= 0 && lastIndex >= rows.length - 1 - 5) onLoadMore()
  }, [lastIndex, rows.length, onLoadMore, isLoadingMore])

  // Column widths: explicit size = fixed px, default (150) = fill remaining space equally
  const allCols = table.getAllColumns()
  const fixedTotal = allCols.reduce((sum, col) => col.getSize() !== 150 ? sum + col.getSize() : sum, 0)
  const autoCount = allCols.filter(col => col.getSize() === 150).length
  const colgroup = allCols.map(col => {
    const size = col.getSize()
    if (size !== 150) return `${size}px`
    return autoCount > 0 ? `calc((100% - ${fixedTotal}px) / ${autoCount})` : 'auto'
  })
  const minWidth = fixedTotal + autoCount * minFillColumnWidth

  return (
    <div className={`flex flex-col overflow-hidden h-full ${className}`}>
      {/* Single scroll container with sticky header */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto overflow-x-hidden max-md:overflow-x-auto">
        <div className="max-md:min-w-(--table-min-width)" style={{ '--table-min-width': `${minWidth}px` } as CSSProperties}>
        <table className="w-full table-fixed border-collapse">
          <colgroup>
            {colgroup.map((w, i) => <col key={i} style={{ width: w }} />)}
          </colgroup>
          <thead className="sticky top-0 z-10">
            {table.getHeaderGroups().map(headerGroup => (
              <tr
                key={headerGroup.id}
                className="bg-muted border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider"
              >
                {headerGroup.headers.map(header => (
                  <th key={header.id} className="px-3 py-2 text-left font-medium">
                    {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
        </table>

        {rows.length === 0 ? (
          <div className="text-center py-8 text-sm text-gray-400 dark:text-gray-500">
            {emptyMessage}
          </div>
        ) : (
          <div style={{ height: `${virtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }}>
            {virtualItems.map(virtualRow => {
              const row = rows[virtualRow.index]
              const rClassName = typeof rowClassName === 'function' ? rowClassName(row.original) : rowClassName
              return (
                <div
                  key={row.id}
                  // In measure mode the virtualizer reads each row's real height
                  // via ResizeObserver (data-index + this ref); the row's own
                  // content sets the height. Otherwise the row is pinned to the
                  // estimated size.
                  ref={measureRows ? virtualizer.measureElement : undefined}
                  data-index={virtualRow.index}
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    ...(measureRows ? {} : { height: `${virtualRow.size}px` }),
                    transform: `translateY(${virtualRow.start}px)`,
                  }}
                >
                  <table className={`w-full table-fixed border-collapse ${measureRows ? '' : 'h-full'}`}>
                    <colgroup>
                      {colgroup.map((w, i) => <col key={i} style={{ width: w }} />)}
                    </colgroup>
                    <tbody>
                      <tr
                        className={`border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800 ${onRowClick ? 'cursor-pointer' : ''} ${rClassName ?? ''}`}
                        onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                      >
                        {row.getVisibleCells().map(cell => (
                          <td key={cell.id} className={`px-3 py-2 overflow-hidden ${measureRows ? 'align-top' : ''}`}>
                            {flexRender(cell.column.columnDef.cell, cell.getContext())}
                          </td>
                        ))}
                      </tr>
                    </tbody>
                  </table>
                </div>
              )
            })}
          </div>
        )}
        {isLoadingMore && (
          <div className="py-2 text-center text-sm text-gray-400">Loading more...</div>
        )}
        </div>
      </div>
    </div>
  )
}
