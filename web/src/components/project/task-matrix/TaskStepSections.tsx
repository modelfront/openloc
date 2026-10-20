// The Tasks tab on the page: one collapsible section per workflow step, each
// holding the jobs whose current task is at that step (lib/task-matrix
// stepSections), all closed at first — except a lone section (a one-step
// workflow, or no workflow at all), which starts open: there is nothing to
// choose between. Jobs with no workflow tasks get a last section of their own.
// A step's header is green once every task at it is completed, and yellow
// while one is in progress (stepTones). It says "Show 3 tasks" /
// "Hide 3 tasks" by its chevron, so a closed one reads as something to open; a
// step with no current tasks says "No tasks" and does not open.
// Expand shows every task.
//
// A section's body is a plain DataTable, one value per cell: fixed widths on
// the short columns, File and Assignee sharing the rest and truncating, so it
// never scrolls either way — it grows the page instead. Lang, File, Deadline
// and Words widen by dragging their header's edge, the same widths in every
// step and remembered per browser; Assignee takes what they leave, and a drag
// stops before it gets too thin. When the table is
// narrow, Words drops out (a container query) before File and Assignee get too
// thin to read. A row's background and left stripe carry its task's state,
// like a matrix cell; the last column's icon says only what the colour
// cannot — an error, a send-back, an AI step running, the comment the task
// was handed — with the text in its tooltip. A click on the row opens the
// job in the CAT editor in a new tab, like the Tasks page (not while its AI
// step runs, which its icon explains), and the tab refreshes when the user
// comes back. Selection is the Files tab's: a checkbox column (the whole cell
// toggles) and a header box for the step.

import type { ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bot, MessageSquareText, Undo2, type LucideIcon } from 'lucide-react'
import type { Table } from '@tanstack/react-table'
import { SelectAllCheckbox } from '@/components/list/SelectAllCheckbox'

import {
  cellTaskIds,
  stepLabel,
  stepSections,
  TASK_STATE_DISPLAY,
  type MatrixCell,
  type MatrixColumn,
  type MatrixRow,
  type TaskMatrix as TaskMatrixModel,
} from '@/lib/task-matrix'
import { cn } from '@/lib/utils'
import { editorPath, openEditorTab } from '@/lib/editor-link'
import { HANDOFF_NOTE_TITLE } from '@/lib/current-task'
import { isAIStepRunning } from '@/lib/project-logic'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { EditableTitle } from '@/components/list/EditableTitle'
import { chipStripeClass, chipSurfaceClass, chipSurfaceHoverClass, type ChipColor } from '@/components/ui/chip'
import { STEP_TYPE_CHIPS } from '@/components/ui/chip-presets'
import { DataTable, type ColumnDef, type DataTableColumnMeta } from '@/components/ui/data-table'
import { LanguageCode } from '@/components/ui/language-code'
import { SectionInfo } from '@/components/ui/section-info'
import { useStepColumnSizing } from './column-sizing'
import { useProjectRefreshOnReturn } from './refresh-on-return'
import { NoteLines, TaskAssignee, TaskDue } from './task-cell-parts'

interface Props {
  projectId: number
  /** Rows already cut down to their current task (currentTaskMatrix). */
  matrix: TaskMatrixModel
  selected: ReadonlySet<number>
  onSelectedChange: (next: Set<number>) => void
  /** Each step header's colour, by step id (stepTones over the whole matrix). */
  tones: ReadonlyMap<number, ChipColor>
}

export function TaskStepSections({ projectId, matrix, selected, onSelectedChange, tones }: Props) {
  const navigate = useNavigate()
  const refreshOnReturn = useProjectRefreshOnReturn(projectId)
  const [columnSizing, setColumnSizing] = useStepColumnSizing()
  const toggle = (ids: number[], on: boolean) => {
    const next = new Set(selected)
    for (const id of ids) {
      if (on) next.add(id)
      else next.delete(id)
    }
    onSelectedChange(next)
  }
  const sections = stepSections(matrix)

  return (
    <div data-testid="task-step-sections">
      {sections.map(({ column, matrix: section }) => {
        const TypeChip = column ? STEP_TYPE_CHIPS[column.type] : null
        const sectionId = column?.stepId ?? 'none'
        const meta: StepTableMeta = {
          projectId,
          column,
          sectionId,
          selected,
          visibleIds: section.rows.flatMap(r => cellTaskIds(currentCell(r, column))),
          onToggle: toggle,
        }
        return (
          <SlimCollapsibleSection
            key={sectionId}
            title={column ? stepLabel(column.name) : 'No workflow step'}
            size="compact"
            variant="grouped"
            tone={column ? tones.get(column.stepId) : undefined}
            disabled={section.rows.length === 0}
            toggleLabel={countLabel(section.rows.length, column ? 'task' : 'job')}
            defaultOpen={sections.length === 1}
            info={TypeChip && <TypeChip iconOnly />}
            data-testid={`task-step-section-${sectionId}`}
          >
            {/* The group draws the line under the last row. */}
            <div className="@container [&_tbody_tr:last-child]:border-b-0">
              <DataTable
                columns={STEP_TABLE_COLUMNS}
                data={section.rows}
                meta={meta as unknown as Record<string, unknown>}
                onRowClick={row => {
                  if (isAIStepRunning(row.job)) return
                  refreshOnReturn()
                  openEditorTab(editorPath(projectId, row.job.id), navigate)
                }}
                rowTestId={row => `task-row-${row.key}`}
                columnSizing={columnSizing}
                onColumnSizingChange={setColumnSizing}
                rowClassName={row => {
                  const cell = currentCell(row, column)
                  const color = cell && TASK_STATE_DISPLAY[cell.state].color
                  return cn(
                    color && [chipSurfaceClass(color), chipSurfaceHoverClass(color)],
                    // Hard-blocked while the AI step runs; Open says why.
                    isAIStepRunning(row.job) && 'cursor-not-allowed',
                  )
                }}
              />
            </div>
          </SlimCollapsibleSection>
        )
      })}
    </div>
  )
}

interface StepTableMeta {
  projectId: number
  /** Null in the section of jobs with no workflow tasks. */
  column: MatrixColumn | null
  sectionId: number | 'none'
  selected: ReadonlySet<number>
  /** Every task the section shows, for the header box. */
  visibleIds: number[]
  onToggle: (ids: number[], on: boolean) => void
}

const metaOf = (table: Table<MatrixRow>) => table.options.meta as unknown as StepTableMeta

const currentCell = (row: MatrixRow, column: MatrixColumn | null): MatrixCell | undefined =>
  column ? row.cells[column.stepId] : undefined

/** "3 tasks", "1 task", "No tasks" — the step header's toggle text. */
const countLabel = (n: number, noun: string) =>
  n === 0 ? `No ${noun}s` : `${n.toLocaleString()} ${n === 1 ? noun : `${noun}s`}`

const resizable = (minSize: number, maxSize: number) => ({ enableResizing: true, minSize, maxSize })

const NONE = <span className="text-gray-400 dark:text-gray-500">—</span>

// Module scope, so cells keep their identity across renders; what changes
// arrives through `meta`. The short columns are fixed; File and Assignee take
// what is left, half each.
const STEP_TABLE_COLUMNS: ColumnDef<MatrixRow>[] = [
  {
    id: 'select',
    size: 36,
    // The whole cell toggles, not just the 14px box.
    meta: {
      // Holds the row's state stripe, drawn on its left edge.
      className: 'relative',
      onCellClick: (row, table) => {
        const { column, selected, onToggle } = metaOf(table)
        const ids = cellTaskIds(currentCell(row, column))
        onToggle(ids, !ids.every(id => selected.has(id)))
      },
      onHeaderClick: table => {
        const { selected, visibleIds, onToggle } = metaOf(table)
        onToggle(visibleIds, !(visibleIds.length > 0 && visibleIds.every(id => selected.has(id))))
      },
    } satisfies DataTableColumnMeta<MatrixRow>,
    header: ({ table }) => {
      const { sectionId, selected, visibleIds, onToggle } = metaOf(table)
      if (visibleIds.length === 0) return null
      const all = visibleIds.every(id => selected.has(id))
      const some = visibleIds.some(id => selected.has(id))
      // A flex box centres the box; inline, it sits on the text baseline.
      return (
        <span className="flex items-center">
          <SelectAllCheckbox
            checked={all}
            indeterminate={some}
            onChange={on => onToggle(visibleIds, on)}
            aria-label="Select every task at this step"
            data-testid={`task-select-all-${sectionId}`}
          />
        </span>
      )
    },
    cell: ({ row, table }) => {
      const { column, selected, onToggle } = metaOf(table)
      const cell = currentCell(row.original, column)
      // A job with no workflow tasks has nothing to select, and no state.
      if (!cell) return null
      const ids = cellTaskIds(cell)
      const { label, color } = TASK_STATE_DISPLAY[cell.state]
      return (
        <span className="flex items-center">
          <span
            aria-hidden="true"
            className={cn(
              'pointer-events-none absolute inset-y-0 left-0 w-0.5 before:absolute before:inset-0',
              chipStripeClass(color),
            )}
          />
          {/* The colour carries the state; screen readers get it as text. */}
          <span className="sr-only" data-testid={`task-status-${row.original.key}`} data-state={cell.state}>{label}</span>
          {ids.length > 0 && (
            <input
              type="checkbox"
              checked={ids.every(id => selected.has(id))}
              onChange={e => onToggle(ids, e.target.checked)}
              className="size-3.5 cursor-pointer accent-primary"
              aria-label={`Select ${row.original.fileName} (${row.original.targetLanguage})`}
              data-testid={`task-select-${row.original.key}`}
            />
          )}
        </span>
      )
    },
  },
  {
    id: 'language',
    header: 'Lang',
    size: 64,
    ...resizable(48, 160),
    cell: ({ row }) => (
      <span className="block truncate" data-testid={`task-row-language-${row.original.key}`}>
        <LanguageCode code={row.original.targetLanguage} className="text-gray-600 dark:text-gray-400" />
      </span>
    ),
  },
  {
    id: 'file',
    header: 'File',
    ...resizable(120, 800),
    cell: ({ row }) => (
      <div className="flex min-w-0">
        <EditableTitle name={row.original.fileName} canEdit={false} className="text-sm text-gray-900 dark:text-gray-100" />
      </div>
    ),
  },
  {
    id: 'assignee',
    header: 'Assignee',
    cell: ({ row, table }) => {
      const { column } = metaOf(table)
      const cell = currentCell(row.original, column)
      return (
        <div className="flex min-w-0 text-sm">
          {cell && column ? <TaskAssignee cell={cell} column={column} /> : NONE}
        </div>
      )
    },
  },
  {
    id: 'deadline',
    header: 'Deadline',
    // Room for a default chip's longest text, a date in another year:
    // "Due Jan 15, 2030".
    size: 144,
    ...resizable(100, 320),
    cell: ({ row, table }) => {
      const { column } = metaOf(table)
      const cell = currentCell(row.original, column)
      return (
        <div className="flex min-w-0 overflow-hidden text-xs">
          {cell && column ? <TaskDue cell={cell} column={column} size="default" /> : NONE}
        </div>
      )
    },
  },
  {
    id: 'words',
    header: () => <span className="block text-right">Words</span>,
    size: 80,
    ...resizable(56, 200),
    meta: { className: '@max-[679px]:hidden' } satisfies DataTableColumnMeta<MatrixRow>,
    cell: ({ row }) => <span className="block truncate text-right text-sm tabular-nums">{row.original.job.word_count.toLocaleString()}</span>,
  },
  {
    id: 'note',
    // One icon, so the column is only as wide as an icon: the row's most
    // important note — an error, a send-back, an AI step running (why the row
    // will not open), the comment the task was handed — with every note's
    // text in its tooltip. The send-back belongs to the job, not to the
    // current cell: the step that sent the job back is a later one.
    size: 40,
    cell: ({ row, table }) => {
      const cell = currentCell(row.original, metaOf(table).column)
      const notes: { key: string; label: string; tone?: 'red'; icon?: LucideIcon; text: ReactNode }[] = []
      if (cell && cell.errors.length > 0) {
        notes.push({ key: 'error', label: 'Error', tone: 'red', text: cell.errors.map(msg => <p key={msg}>{msg}</p>) })
      }
      const { sentBack } = row.original
      if (sentBack) {
        notes.push({
          key: 'sent-back', label: 'Sent back', icon: Undo2,
          text: <><p className="font-medium">Sent back from {stepLabel(sentBack.stepName)}</p>{sentBack.reason && <p>{sentBack.reason}</p>}</>,
        })
      }
      if (isAIStepRunning(row.original.job)) {
        notes.push({ key: 'ai', label: 'AI step running', icon: Bot, text: <p>The AI step is running. The job opens when it finishes.</p> })
      }
      if (cell && cell.handoffNotes.length > 0) {
        notes.push({
          key: 'comment', label: 'Comment', icon: MessageSquareText,
          text: <NoteLines title={HANDOFF_NOTE_TITLE} lines={cell.handoffNotes} />,
        })
      }
      if (notes.length === 0) return null
      const [top] = notes
      return (
        <span className="flex items-center">
          <SectionInfo
            tone={top.tone}
            icon={top.icon}
            label={notes.map(n => n.label).join(', ')}
            data-testid={`task-note-${top.key}-${row.original.key}`}
          >
            <div className="flex flex-col gap-1.5">{notes.map(n => <div key={n.key}>{n.text}</div>)}</div>
          </SectionInfo>
        </span>
      )
    },
  },
]
