// One task-matrix cell (the Expand view): one job's task for one step. It
// shows who has it, when it is due and how many words; the background carries
// the state and turns blue when selected.

import { memo } from 'react'

import { cn } from '@/lib/utils'
import {
  TASK_STATE_DISPLAY,
  type CellSelection,
  type MatrixCell,
  type MatrixColumn,
} from '@/lib/task-matrix'
import { chipStripeClass, chipSurfaceClass, chipTextClass } from '@/components/ui/chip'
import { CountMeta } from '@/components/ui/count-meta'
import { CommentNote, ErrorTip, SentBackNote, TaskAssignee, TaskDue } from './task-cell-parts'

interface Props {
  cell: MatrixCell
  column: MatrixColumn
  selection: CellSelection
  onSelect: (cell: MatrixCell, e: React.MouseEvent | React.KeyboardEvent) => void
}

function TaskMatrixCellImpl({ cell, column, selection, onSelect }: Props) {
  const display = TASK_STATE_DISPLAY[cell.state]
  const selectable = !cell.dimmed

  return (
    <td
      role="gridcell"
      aria-selected={selection === 'all'}
      aria-disabled={!selectable || undefined}
      tabIndex={selectable ? 0 : -1}
      data-testid={`task-cell-${cell.key}`}
      data-state={cell.state}
      data-selected={selection}
      onClick={selectable ? e => onSelect(cell, e) : undefined}
      onKeyDown={selectable ? e => {
        if (e.key !== 'Enter' && e.key !== ' ') return
        e.preventDefault()
        onSelect(cell, e)
      } : undefined}
      className={cn(
        // The ::after layer is the hover tint: it sits over whatever the state
        // background is, so hovering never hides the state colour.
        'relative h-full border-b border-r border-gray-200 p-0 align-top outline-none dark:border-gray-800',
        'after:pointer-events-none after:absolute after:inset-0 after:bg-primary/10 after:opacity-0',
        // The ::before layer is the state's stripe down the left edge.
        'before:pointer-events-none before:absolute before:inset-y-0 before:left-0 before:w-0.5',
        chipStripeClass(display.color),
        'focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
        selectable && 'cursor-pointer hover:after:opacity-100',
        cell.dimmed && 'opacity-40',
        selection === 'all' ? 'bg-primary/20 ring-2 ring-inset ring-primary' : chipSurfaceClass(display.color),
        selection === 'some' && 'ring-2 ring-inset ring-primary/40',
      )}
    >
      <div className="flex h-full min-w-0 flex-col justify-between gap-1 px-2.5 py-1.5 text-xs">
        {/* The colour carries the state; screen readers get it as text. */}
        <span className="sr-only">{display.label}</span>
        <div className="flex min-w-0 items-center">
          <TaskAssignee cell={cell} column={column} />
        </div>

        <div className="flex min-w-0 items-center justify-between gap-2">
          <TaskDue cell={cell} column={column} />
          <CountMeta items={[{ count: cell.words, noun: 'word' }]} className="ml-auto shrink-0 text-gray-500 dark:text-gray-400" />
        </div>

        {cell.errors.length > 0 ? (
          <ErrorTip errors={cell.errors}>
            <span className={cn('truncate font-medium', chipTextClass('red'))} data-testid="task-cell-error">
              {cell.errors[0]}
            </span>
          </ErrorTip>
        ) : cell.sentBack > 0 ? (
          <SentBackNote cell={cell} />
        ) : (
          <CommentNote cell={cell} />
        )}
      </div>
    </td>
  )
}

// Cells come out of a memoized matrix build, so an unchanged cell keeps its
// identity across renders; only a selection change or a rebuild re-renders it.
export const TaskMatrixCell = memo(
  TaskMatrixCellImpl,
  (a, b) => a.cell === b.cell && a.column === b.column && a.selection === b.selection && a.onSelect === b.onSelect,
)
