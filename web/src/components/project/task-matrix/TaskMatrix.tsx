// The task matrix grid (the Tasks tab's Expand view): workflow steps across,
// jobs down, every task of every job.
//
// One <table> in one scroll container, a sticky header row and a sticky first
// column; rows are virtualized with spacer rows above and below, so a project
// with thousands of jobs renders only what is on screen. Rows share one fixed
// height, so the virtualizer never measures them.
//
// Selection: click selects a cell's tasks, Ctrl/Cmd+click adds or removes a
// cell. A row header or number selects its row and a step header its column,
// with the same keys. Escape clears.

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react'
import { useReactTable, getCoreRowModel, flexRender, type ColumnDef } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'

import type { Job } from '@/api'
import { cn } from '@/lib/utils'
import {
  applySelection,
  cellSelection,
  cellTaskIds,
  columnTaskIds,
  rowHasNotes,
  languageLabelRows,
  languageRuns,
  rowTaskIds,
  selectedTasks,
  selectModeFor,
  stepLabel,
  type MatrixCell,
  type MatrixColumn,
  type MatrixRow,
  type TaskMatrix as TaskMatrixModel,
} from '@/lib/task-matrix'
import { EditableTitle } from '@/components/list/EditableTitle'
import { STEP_TYPE_CHIPS } from '@/components/ui/chip-presets'
import { LanguageCode } from '@/components/ui/language-code'
import { OpenJobButton } from './OpenJobButton'
import { TaskMatrixCell } from './TaskMatrixCell'
import { useColumnSizing } from './column-sizing'
import { ColumnResizeHandle } from '@/components/ui/column-resize-handle'
import { useProjectRefreshOnReturn } from './refresh-on-return'

// Per row: two lines (assignee; deadline + words), three when one of its cells
// has a note (lib/task-matrix cellHasNote). Heights are computed, never
// measured, so the virtualizer knows each row before it renders it.
const TWO_LINES = 52
const EXTRA_LINE = 20
// The language and file columns together are 310px, each step 190px. The
// language column holds only a code. File names truncate to a tooltip.
// Columns never stretch: an empty filler column takes the space they leave.
// These are the defaults: every column but # is resizable by dragging its
// header's right edge (column-sizing.ts remembers the widths), within
// [minWidth, MAX_COLUMN_WIDTH].
const HEAD_WIDTH = 310
const STEP_WIDTH = 190
const LANGUAGE_COLUMN_WIDTH = 100
const MIN_WIDTH = { language: 48, name: 80, step: 120 }
const MAX_COLUMN_WIDTH = 800

/** The sticky columns before the steps. */
type HeadColumnId = 'language' | 'file'
interface HeadColumn { id: HeadColumnId; label: string; width: number; minWidth: number }

const clampWidth = (w: number, min: number) => Math.min(MAX_COLUMN_WIDTH, Math.max(min, w))

// The row-number gutter: as wide as the largest number needs. Pixels, not
// `ch`, because the sticky name column is offset by exactly this much.
const DIGIT_PX = 7
const numberColumnWidth = (rows: number) => Math.max(2, String(rows).length) * DIGIT_PX + 12

type SelectEvent = React.MouseEvent | React.KeyboardEvent

interface Props {
  projectId: number
  matrix: TaskMatrixModel
  selected: ReadonlySet<number>
  onSelectedChange: (next: Set<number>) => void
  /** Sizes the scroll container. */
  className?: string
}

export function TaskMatrix({ projectId, matrix, selected, onSelectedChange, className }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)

  // Open starts the editor in a new tab. Armed here, not in the row: a
  // virtualized row can unmount while the user is away.
  const refreshOnReturn = useProjectRefreshOnReturn(projectId)

  // Handlers read the latest matrix + selection through a ref, so their
  // identity stays stable and memoized cells skip re-rendering.
  const stateRef = useRef({ matrix, selected, onSelectedChange })
  useLayoutEffect(() => {
    stateRef.current = { matrix, selected, onSelectedChange }
  })

  const selectIds = useCallback((ids: number[], e: SelectEvent) => {
    const { selected, onSelectedChange } = stateRef.current
    onSelectedChange(applySelection(selected, ids, selectModeFor(e)))
  }, [])

  const onCellSelect = useCallback((cell: MatrixCell, e: SelectEvent) => {
    selectIds(cellTaskIds(cell), e)
  }, [selectIds])

  const numberWidth = numberColumnWidth(matrix.rows.length)
  const [columnSizing, setColumnSizing] = useColumnSizing()
  const headSpec = useMemo<HeadColumn[]>(() => [
    { id: 'language', label: 'Language', width: LANGUAGE_COLUMN_WIDTH, minWidth: MIN_WIDTH.language },
    { id: 'file', label: 'File', width: HEAD_WIDTH - LANGUAGE_COLUMN_WIDTH, minWidth: MIN_WIDTH.name },
  ], [])
  // The same clamp TanStack applies to getSize(), computed from the sizing
  // state so the widths are plain values the memos below can depend on.
  const headColumns = useMemo(() => {
    let left = numberWidth
    return headSpec.map(c => {
      const width = clampWidth(columnSizing[c.id] ?? c.width, c.minWidth)
      const col = { ...c, width, left }
      left += width
      return col
    })
  }, [headSpec, columnSizing, numberWidth])
  const stepWidths = useMemo(
    () => new Map(matrix.columns.map(c => [c.stepId, clampWidth(columnSizing[`step-${c.stepId}`] ?? STEP_WIDTH, MIN_WIDTH.step)])),
    [matrix.columns, columnSizing],
  )
  const tableWidth = numberWidth
    + headColumns.reduce((n, c) => n + c.width, 0)
    + [...stepWidths.values()].reduce((n, w) => n + w, 0)
  const runs = useMemo(() => languageRuns(matrix.rows), [matrix.rows])

  const columns = useMemo<ColumnDef<MatrixRow>[]>(() => [
    { id: 'number', header: '#', size: numberWidth, enableResizing: false, meta: { sticky: 'number' } },
    ...headSpec.map((c): ColumnDef<MatrixRow> => ({
      id: c.id, header: c.label, size: c.width, minSize: c.minWidth, maxSize: MAX_COLUMN_WIDTH, meta: { sticky: 'row' },
    })),
    ...matrix.columns.map((col): ColumnDef<MatrixRow> => ({
      id: `step-${col.stepId}`,
      header: () => <StepHeader matrix={matrix} column={col} />,
      size: STEP_WIDTH, minSize: MIN_WIDTH.step, maxSize: MAX_COLUMN_WIDTH,
      meta: { column: col },
    })),
  ], [matrix, headSpec, numberWidth])

  const table = useReactTable({
    data: matrix.rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: r => r.key,
    state: { columnSizing },
    onColumnSizingChange: setColumnSizing,
    enableColumnResizing: true,
    columnResizeMode: 'onChange',
  })
  const rows = table.getRowModel().rows

  // A drag that ends over its header must not also click it (select the step).
  const suppressHeaderClick = useRef(false)
  const startResize = (handler: (e: unknown) => void) => (e: React.MouseEvent | React.TouchEvent) => {
    e.stopPropagation()
    if ('button' in e) e.preventDefault() // no text selection while dragging
    suppressHeaderClick.current = true
    window.addEventListener('mouseup', () => setTimeout(() => { suppressHeaderClick.current = false }), { once: true })
    handler(e)
  }

  const rowHeights = useMemo(
    () => matrix.rows.map(r => TWO_LINES + (rowHasNotes(r) ? EXTRA_LINE : 0)),
    [matrix.rows],
  )
  const labelRows = useMemo(() => languageLabelRows(runs, rowHeights), [runs, rowHeights])
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: i => rowHeights[i] ?? TWO_LINES,
    overscan: 8,
  })
  // The size cache survives an option change, so re-measure when it flips.
  useEffect(() => { virtualizer.measure() }, [virtualizer, rowHeights])
  const items = virtualizer.getVirtualItems()
  const padTop = items.length > 0 ? items[0].start : 0
  const padBottom = items.length > 0 ? virtualizer.getTotalSize() - items[items.length - 1].end : 0
  const colSpan = matrix.columns.length + headColumns.length + 2

  return (
    <div
      ref={scrollRef}
      className={cn('overflow-auto rounded-lg border border-gray-200 bg-card dark:border-gray-800', className)}
      // Shift+click would stretch the page's text highlight across the grid.
      onMouseDown={e => { if (e.shiftKey) e.preventDefault() }}
      onKeyDown={e => {
        // Only a selection the user can see counts; with none, Escape carries
        // on to whatever else it closes (the full-screen modal).
        if (e.key !== 'Escape' || selectedTasks(matrix, selected).length === 0) return
        e.stopPropagation()
        onSelectedChange(new Set())
      }}
      data-testid="task-matrix"
    >
      <table
        role="grid"
        aria-multiselectable="true"
        aria-rowcount={rows.length + 1}
        className="w-full table-fixed border-separate border-spacing-0"
        style={{ minWidth: tableWidth }}
      >
        <colgroup>
          <col style={{ width: numberWidth }} />
          {headColumns.map(c => <col key={c.id} style={{ width: c.width }} data-testid={`task-matrix-col-${c.id}`} />)}
          {matrix.columns.map(c => (
            <col key={c.stepId} style={{ width: stepWidths.get(c.stepId) }} data-testid={`task-matrix-col-step-${c.stepId}`} />
          ))}
          <col />
        </colgroup>
        <thead className="sticky top-0 z-20">
          {table.getHeaderGroups().map(group => (
            <tr key={group.id} className="bg-muted text-left text-xs font-medium text-gray-500 dark:text-gray-400">
              {group.headers.map(header => {
                const meta = header.column.columnDef.meta as { column?: MatrixColumn; sticky?: 'number' | 'row' } | undefined
                const col = meta?.column
                const head = headColumns.find(c => c.id === header.column.id)
                return (
                  <th
                    key={header.id}
                    scope="col"
                    style={head ? { left: head.left } : undefined}
                    className={cn(
                      'relative border-b border-r border-gray-200 bg-muted py-2 font-medium dark:border-gray-800',
                      col && 'cursor-pointer px-2.5 hover:bg-primary/10',
                      meta?.sticky === 'number' && 'sticky left-0 z-10 px-1.5 text-center',
                      meta?.sticky === 'row' && 'sticky z-10 px-2',
                      head?.id === 'language' && 'text-center',
                    )}
                    onClick={col ? e => {
                      if (suppressHeaderClick.current) { suppressHeaderClick.current = false; return }
                      selectIds(columnTaskIds(matrix, col.stepId), e)
                    } : undefined}
                    title={col ? 'Select this step' : undefined}
                    data-testid={col ? `task-matrix-step-${col.stepId}` : undefined}
                  >
                    {flexRender(header.column.columnDef.header, header.getContext())}
                    {header.column.getCanResize() && (
                      // Drag to resize; double-click restores the default.
                      <ColumnResizeHandle
                        active={header.column.getIsResizing()}
                        onMouseDown={startResize(header.getResizeHandler())}
                        onTouchStart={startResize(header.getResizeHandler())}
                        onClick={e => e.stopPropagation()}
                        onDoubleClick={e => { e.stopPropagation(); header.column.resetSize() }}
                        data-testid={`task-matrix-resize-${header.column.id}`}
                      />
                    )}
                  </th>
                )
              })}
              <th aria-hidden="true" className="bg-card" />
            </tr>
          ))}
        </thead>
        <tbody>
          {padTop > 0 && <tr style={{ height: padTop }}><td colSpan={colSpan} /></tr>}
          {items.map(item => {
            const row = rows[item.index].original
            return (
              <tr key={row.key} style={{ height: rowHeights[item.index] }} aria-rowindex={item.index + 2} data-testid={`task-row-${row.key}`}>
                {/* Position in the current order, so it follows the filters.
                    A click selects the row, like its header. */}
                <td
                  aria-hidden="true"
                  className="sticky left-0 z-10 cursor-pointer border-b border-r border-gray-200 bg-card px-1.5 text-center align-middle text-xs tabular-nums text-gray-400 hover:bg-muted dark:border-gray-800 dark:text-gray-500"
                  onClick={e => selectIds(rowTaskIds(row), e)}
                  data-testid="task-row-number"
                >
                  {(item.index + 1).toLocaleString()}
                </td>
                {headColumns.map(c => {
                  // The language reads as one cell over its run: the code sits
                  // at the run's middle (in the row holding it, lifted above its
                  // neighbours so it may cross their line), no line inside the
                  // run, and a click selects it all.
                  const run = c.id === 'language' ? runs[item.index] : undefined
                  const labelTop = run ? labelRows.get(item.index) : undefined
                  return (
                  <th
                    key={c.id}
                    scope="row"
                    style={{ left: c.left }}
                    className={cn(
                      'sticky z-10 cursor-pointer border-b border-r border-gray-200 bg-card px-2 py-1.5 text-left font-normal dark:border-gray-800',
                      run ? labelTop != null && 'z-[11]' : 'align-middle hover:bg-muted',
                      run && !run.end && 'border-b-transparent dark:border-b-transparent',
                    )}
                    onClick={e => selectIds(run ? run.taskIds : rowTaskIds(row), e)}
                    // The file cell keeps the row-header id: it names the row
                    // and holds its Open button.
                    data-testid={c.id === 'language' ? `task-row-language-${row.key}` : `task-row-header-${row.key}`}
                  >
                    {c.id === 'language' ? (
                      labelTop != null && (
                        <LanguageCode
                          code={row.targetLanguage}
                          style={{ top: labelTop }}
                          className="pointer-events-none absolute inset-x-0 -translate-y-1/2 text-center whitespace-nowrap text-gray-600 dark:text-gray-400"
                        />
                      )
                    ) : (
                      <JobFileCell projectId={projectId} job={row.job} onOpen={refreshOnReturn} />
                    )}
                  </th>
                  )
                })}
                {matrix.columns.map(col => {
                  const cell = row.cells[col.stepId]
                  return cell ? (
                    <TaskMatrixCell
                      key={col.stepId}
                      cell={cell}
                      column={col}
                      selection={cellSelection(cell, selected)}
                      onSelect={onCellSelect}
                    />
                  ) : (
                    <td key={col.stepId} className="border-b border-r border-gray-200 text-center text-xs text-gray-300 dark:border-gray-800 dark:text-gray-600">
                      —
                    </td>
                  )
                })}
                <td aria-hidden="true" />
              </tr>
            )
          })}
          {padBottom > 0 && <tr style={{ height: padBottom }}><td colSpan={colSpan} /></tr>}
        </tbody>
      </table>
    </div>
  )
}

function StepHeader({ matrix, column }: { matrix: TaskMatrixModel; column: MatrixColumn }) {
  const TypeChip = STEP_TYPE_CHIPS[column.type]
  let done = 0
  let total = 0
  for (const r of matrix.rows) {
    const c = r.cells[column.stepId]
    if (c) { done += c.done; total += c.total }
  }
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <span className="truncate text-sm text-gray-900 dark:text-gray-100">{stepLabel(column.name)}</span>
      <TypeChip iconOnly />
      <span className="ml-auto shrink-0 tabular-nums">{done}/{total}</span>
    </span>
  )
}

/** A row's file: its name, and Open. */
function JobFileCell({ projectId, job, onOpen }: { projectId: number; job: Job; onOpen: () => void }) {
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <EditableTitle name={job.file_name} canEdit={false} className="text-sm text-gray-900 dark:text-gray-100" />
      <OpenJobButton projectId={projectId} job={job} onOpen={onOpen} className="ml-auto" />
    </div>
  )
}
