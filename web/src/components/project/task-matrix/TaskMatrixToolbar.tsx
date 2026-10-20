// Controls above the task matrix: search, the filter chips, the colour legend
// (with a count per state) and the button that opens the matrix full screen.
// Search and filter values live in the URL (lib/task-matrix.ts).
//
// On the page a chip shows only while its filter is set: the filter icon
// (AddFilterMenu) lists the other filters and opens the picked one's options
// right away, and clearing a value — its ×, or "All" — takes the chip off.
// The expanded view shows every chip, always, with neither icon nor ×.

import { useMemo, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Maximize2, Search } from 'lucide-react'

import type { ProjectTask } from '@/api'
import { cn } from '@/lib/utils'
import {
  ALL_TASK_FILTER_KEYS,
  DUE_FILTERS,
  DUE_FILTER_LABELS,
  TASK_FILTERS,
  TASK_STATES,
  TASK_STATE_DISPLAY,
  parseTaskMatrixFilters,
  type TaskFilterKey,
  type TaskMatrix,
  type TaskState,
} from '@/lib/task-matrix'
import { AddFilterMenu, FilterChip, FilterOption } from '@/components/list'
import { useUrlParamSetters } from '@/hooks/useUrlParamSetters'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatDate } from '@/utils'
import { TaskStateChip } from './TaskStateChip'

export interface TaskMatrixFile {
  id: number
  name: string
  uploadedAt: string
}

interface Props {
  matrix: TaskMatrix
  /** Every task of the project, so the option lists don't shrink as filters narrow. */
  tasks: ProjectTask[]
  targets: string[]
  /** The project's files, for the File filter. */
  files: TaskMatrixFile[]
  /** Omitted inside the full-screen modal, which has its own close button. */
  onExpand?: () => void
  /** The full-screen view: every filter chip, a wider search, and the grid's help text at the far right. */
  fullScreen?: boolean
}

const filterLabel = (key: TaskFilterKey) => TASK_FILTERS.find(f => f.key === key)!.label
const filterParam = (key: TaskFilterKey) => TASK_FILTERS.find(f => f.key === key)!.param

export function TaskMatrixToolbar({ matrix, tasks, targets, files, onExpand, fullScreen = false }: Props) {
  const [params] = useSearchParams()
  const filters = useMemo(() => parseTaskMatrixFilters(params), [params])

  const assignees = useMemo(() => {
    const byId = new Map<number, string>()
    for (const t of tasks) if (t.assigned_to != null) byId.set(t.assigned_to, t.assignee_name ?? `User ${t.assigned_to}`)
    return [...byId].sort((a, b) => a[1].localeCompare(b[1]))
  }, [tasks])

  const stateCounts = useMemo(() => {
    const counts: Record<TaskState, number> = { not_started: 0, in_progress: 0, completed: 0, error: 0 }
    for (const r of matrix.rows) for (const c of Object.values(r.cells)) for (const t of c?.tasks ?? []) counts[t.state]++
    return counts
  }, [matrix])

  const { setParam } = useUrlParamSetters()

  const assigneeLabel = filters.assignee === 'none'
    ? 'Unassigned'
    : filters.assignee != null ? (assignees.find(([id]) => id === filters.assignee)?.[1] ?? 'Unknown') : null

  const search = (
    <Input
      type="text"
      value={filters.q ?? ''}
      onChange={e => setParam('q', e.target.value)}
      placeholder="Search files, languages, people…"
      data-testid="task-matrix-search"
      icon={<Search className="h-3.5 w-3.5" />}
      className={cn('h-7', fullScreen ? 'w-96' : 'w-80')}
      inputClassName="text-xs"
    />
  )
  // File is only worth a chip when there is more than one file to pick from.
  const available = ALL_TASK_FILTER_KEYS.filter(k => k !== 'file' || files.length > 1)
  const isSet = (k: TaskFilterKey) => filters[k] != null
  const shown = fullScreen ? available : available.filter(isSet)
  // The states the legend and the Status filter offer: the page leaves Not
  // started out, the expanded view has every state.
  const states = fullScreen ? TASK_STATES : TASK_STATES.filter(s => s !== 'not_started')
  // A legend chip toggles the Status filter to its state; set, it is what
  // puts the Status chip on the page's bar.
  const toggleState = (state: TaskState) => setParam('task_status', filters.state === state ? null : state)

  // Each filter once: its chip's value and its option rows, which the chip and
  // the page's AddFilterMenu both show.
  const option = (selected: boolean, param: string, value: string | null, close: () => void, label: ReactNode, key?: string | number) => (
    <FilterOption key={key} selected={selected} onClick={() => { setParam(param, value); close() }}>{label}</FilterOption>
  )
  const defs: Record<TaskFilterKey, { testId: string; value: string | null; options: (close: () => void) => ReactNode }> = {
    state: {
      testId: 'task-filter-status',
      value: filters.state ? TASK_STATE_DISPLAY[filters.state].label : null,
      options: close => (
        <>
          {option(!filters.state, 'task_status', null, close, 'All')}
          {states.map(s => option(filters.state === s, 'task_status', s, close, TASK_STATE_DISPLAY[s].label, s))}
        </>
      ),
    },
    file: {
      testId: 'task-filter-file',
      value: filters.file != null ? (files.find(f => f.id === filters.file)?.name ?? 'Unknown') : null,
      options: close => (
        <>
          {option(filters.file == null, 'file', null, close, 'All')}
          {files.map(f => option(filters.file === f.id, 'file', String(f.id), close, (
            <span className="flex min-w-0 items-baseline justify-between gap-3">
              <span className="truncate">{f.name}</span>
              {/* Names repeat across uploads; the date tells them apart. */}
              <span className="shrink-0 text-xs text-gray-400 dark:text-gray-500">{formatDate(f.uploadedAt)}</span>
            </span>
          ), f.id))}
        </>
      ),
    },
    target: {
      testId: 'task-filter-language',
      value: filters.target ?? null,
      options: close => (
        <>
          {option(!filters.target, 'target', null, close, 'All')}
          {targets.map(t => option(filters.target === t, 'target', t, close, t, t))}
        </>
      ),
    },
    assignee: {
      testId: 'task-filter-assignee',
      value: assigneeLabel,
      options: close => (
        <>
          {option(filters.assignee == null, 'assignee', null, close, 'All')}
          {option(filters.assignee === 'none', 'assignee', 'none', close, 'Unassigned')}
          {assignees.map(([id, name]) => option(filters.assignee === id, 'assignee', String(id), close, name, id))}
        </>
      ),
    },
    due: {
      testId: 'task-filter-due',
      value: filters.due ? DUE_FILTER_LABELS[filters.due] : null,
      options: close => (
        <>
          {option(!filters.due, 'due', null, close, 'Any')}
          {DUE_FILTERS.map(d => option(filters.due === d, 'due', d, close, DUE_FILTER_LABELS[d], d))}
        </>
      ),
    },
  }

  const filterChips = (
    <>
      {!fullScreen && available.some(k => !isSet(k)) && (
        <AddFilterMenu
          testId="task-filter-menu"
          filters={available.filter(k => !isSet(k)).map(k => ({ key: k, label: filterLabel(k), options: defs[k].options }))}
        />
      )}
      {shown.map(k => (
        <FilterChip
          key={k}
          label={filterLabel(k)}
          value={defs[k].value}
          testId={defs[k].testId}
          // On the page the × clears the filter, which takes the chip off.
          onRemove={fullScreen ? undefined : () => setParam(filterParam(k), null)}
          render={defs[k].options}
        />
      ))}
    </>
  )
  const expandButton = onExpand && (
    <Button
      size="sm"
      className="ml-auto"
      onClick={onExpand}
      startIcon={<Maximize2 className="size-3.5" />}
      data-testid="task-matrix-expand"
    >
      Expand
    </Button>
  )

  return (
    <div className="flex flex-col gap-2" data-testid="task-matrix-toolbar">
      <div className="flex flex-wrap items-center gap-2">
        {search}
        {filterChips}
        {expandButton}
      </div>

      <div className="flex flex-wrap items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400" data-testid="task-matrix-legend">
        <span>Tasks:</span>
        {states.map(s => (
          <TaskStateChip key={s} state={s} count={stateCounts[s]} pressed={filters.state === s} onClick={() => toggleState(s)} data-testid={`task-legend-${s}`} />
        ))}
        {/* Only the grid needs a guide; the page's rows work like any list. */}
        {fullScreen && (
          <span className="ml-auto" data-testid="task-matrix-help">
            Click to select · Ctrl/⌘+click to add · Click a header to select all · Esc to clear
          </span>
        )}
      </div>
    </div>
  )
}
