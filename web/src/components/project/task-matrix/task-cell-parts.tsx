// What a task shows, shared by the matrix cell (Expand view) and the step
// tables on the Tasks tab, so the two always say the same thing.

import { Fragment } from 'react'

import { cn } from '@/lib/utils'
import { HANDOFF_NOTE_TITLE } from '@/lib/current-task'
import { deadlineChipProps } from '@/lib/deadline'
import { TASK_STATE_DISPLAY, type MatrixCell, type MatrixColumn } from '@/lib/task-matrix'
import { Chip, chipTextClass, type ChipProps } from '@/components/ui/chip'
import { OverflowChip, STEP_TYPE_PRESETS } from '@/components/ui/chip-presets'
import { CountMeta } from '@/components/ui/count-meta'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { isAutomatedStep } from '@/lib/workflow-steps'

const shortDate = (iso: string) =>
  new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

/**
 * When it is due, or when it was done, as a chip: the deadline's band colour,
 * or the completed state's green with the date it was done. An AI step runs by
 * itself and takes no deadline. `size`: small in a matrix cell, default in a
 * step table row.
 */
export function TaskDue({ cell, column, size = 'small' }: { cell: MatrixCell; column: MatrixColumn; size?: ChipProps['size'] }) {
  if (cell.state === 'completed') {
    return (
      <Chip
        size={size}
        color={TASK_STATE_DISPLAY.completed.color}
        title={cell.completedAt ? `Done ${new Date(cell.completedAt).toLocaleString()}` : undefined}
        data-testid="task-done-chip"
      >
        {cell.completedAt ? `Done ${shortDate(cell.completedAt)}` : 'Done'}
      </Chip>
    )
  }
  if (isAutomatedStep(column.type)) return null
  return <Chip size={size} {...deadlineChipProps(cell.deadline)} />
}

/** Notes people wrote, under an optional title, line breaks kept. */
export function NoteLines({ title, lines }: { title?: string; lines: string[] }) {
  return (
    <>
      {title && <p className="font-medium">{title}</p>}
      {lines.map(line => <p key={line} className="whitespace-pre-line">{line}</p>)}
    </>
  )
}

/** A trigger whose tooltip gives NoteLines. */
function NoteTip({ title, lines, delay, children }: {
  title?: string
  lines: string[]
  delay?: number
  children: React.ReactElement
}) {
  return (
    <Tooltip>
      <TooltipTrigger delay={delay} render={children} />
      <TooltipContent className="max-w-sm">
        <NoteLines title={title} lines={lines} />
      </TooltipContent>
    </Tooltip>
  )
}

export function ErrorTip({ errors, children }: { errors: string[]; children: React.ReactElement }) {
  // An error is what you came to read: no hover delay.
  return <NoteTip lines={errors} delay={0}>{children}</NoteTip>
}

/** A note line in the task's colour: a plain element, so a tooltip can attach to it. */
const noteLine = (cell: MatrixCell, text: string) => (
  <span className={cn('truncate font-medium', chipTextClass(TASK_STATE_DISPLAY[cell.state].color))}>{text}</span>
)

/** "Sent back" in the task's colour; hover shows the reviewer's reason. */
export function SentBackNote({ cell }: { cell: MatrixCell }) {
  if (cell.sentBack === 0) return null
  const line = noteLine(cell, 'Sent back')
  if (cell.rejectionReasons.length === 0) return line
  return <NoteTip lines={cell.rejectionReasons}>{line}</NoteTip>
}

/** "Comment" in the task's colour; hover shows the comment the step before left. */
export function CommentNote({ cell }: { cell: MatrixCell }) {
  if (cell.handoffNotes.length === 0) return null
  return <NoteTip title={HANDOFF_NOTE_TITLE} lines={cell.handoffNotes}>{noteLine(cell, 'Comment')}</NoteTip>
}

/** Who has the task: "Automated" on an automated step, else the assignee or "Unassigned". */
export function TaskAssignee({ cell, column }: { cell: MatrixCell; column: MatrixColumn }) {
  if (isAutomatedStep(column.type)) {
    const Icon = STEP_TYPE_PRESETS[column.type].Icon
    return (
      <span className="inline-flex items-center gap-1 text-gray-500 dark:text-gray-400">
        <Icon className="size-3" aria-hidden="true" /> Automated
      </span>
    )
  }
  const people = cell.assignees
  if (people.length === 0) {
    return <span className="text-gray-400 italic dark:text-gray-500">Unassigned</span>
  }
  // With unassigned tasks, those lead ("3 unassigned") and "+N" counts the
  // people; otherwise the first person leads and "+N" counts the rest.
  const lead = cell.unassigned > 0 ? `${cell.unassigned} unassigned` : people[0].name
  const more = cell.unassigned > 0 ? people.length : people.length - 1
  const line = (
    <span className="flex min-w-0 items-center gap-1" data-testid="task-cell-assignee">
      <span
        className={cell.unassigned > 0
          ? 'truncate text-gray-400 italic dark:text-gray-500'
          : 'truncate font-medium text-gray-900 dark:text-gray-100'}
      >
        {lead}
      </span>
      {more > 0 && <OverflowChip count={more} size="small" />}
    </span>
  )
  if (more === 0) return line
  // "+N" hides people, so hovering lists everyone with their share of tasks.
  return (
    <Tooltip>
      <TooltipTrigger render={line} />
      <TooltipContent className="max-w-sm">
        <span className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5" data-testid="task-cell-assignees">
          {people.map(p => (
            <Fragment key={p.name}>
              <span>{p.name}</span>
              <CountMeta items={[{ count: p.count, noun: 'task' }]} className="text-right opacity-70" />
            </Fragment>
          ))}
          {cell.unassigned > 0 && (
            <>
              <span className="italic">Unassigned</span>
              <CountMeta items={[{ count: cell.unassigned, noun: 'task' }]} className="text-right opacity-70" />
            </>
          )}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}
