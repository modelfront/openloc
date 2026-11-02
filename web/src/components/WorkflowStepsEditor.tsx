// WorkflowStepsEditor — the ordered step list of a project or a library
// workflow, as an editor. Controlled: the caller holds the steps and gets the
// whole list back on every change.
//
// Steps are picked from the step library, never typed: a row's identity
// (name, type, engine) is the definition's; the editor owns order and the
// default assignee, and on a project each step's deadline. A pre-library row
// whose name is not in the library keeps its identity through its own id and
// shows as "(custom)" until swapped.
//
// Locked rows (a project's steps that jobs have reached) come first and stay
// put: the rows after them sort among themselves and can never be dropped
// above them.
//
// Assignee picker filtering:
//   - PROJECT context (sourceLang + targetLangs): users qualified for AT LEAST
//     ONE of the project's target pairs, UNION admins/owners (always).
//   - LIBRARY context (no language props): ALL active members.
// An `ai` step has nobody on it, so its assignee cell reads "Automated".

import { useMemo, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { arrayMove } from '@dnd-kit/sortable'
import { useEngineCreateActions } from '../hooks/useEngineCreateActions'
import { useQuery, useQueries } from '@tanstack/react-query'
import { listAIRouters, listMembers, listUsersByLanguagePair, listStepDefinitions, type Member, type StepType } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isManagerRole } from '../lib/roles'
import { AddItemButton } from '@/components/list'
import { SortableGridRow } from '@/components/list/SortableGridRow'
import { SortableList } from '@/components/list/SortableList'
import { DragHandle } from '@/components/ui/drag-handle'
import { STEP_TYPE_CHIPS, HumanStepChip, type StepTypePresetKey } from '@/components/ui/chip-presets'
import { isAutomatedStep } from '@/lib/workflow-steps'
import { WorkflowStepRow } from './workflow/WorkflowStepRow'
import { useStepLanguageDisclosure } from './workflow/WorkflowDeadlines'

export interface WorkflowStep {
  /** Existing profile row id. A row without a library reference is carried
   *  forward through this id. */
  id?: number
  /** Library definition this row snapshots; null for a pre-library row or an
   *  unpicked placeholder. */
  step_definition_id: number | null
  name: string
  step_type: StepType
  /** AI router the definition carries (hybrid / ai). */
  ai_router_id?: number | null
  default_assignee?: number
  /** Default deadline (ISO 8601) — inherited by assignments on file upload.
   * Projects only; null/undefined = none. */
  deadline?: string | null
  /** Per-target-language overrides of `deadline`, ISO 8601 by language code.
   *  A language absent from the map uses the step's own date. */
  language_deadlines?: Record<string, string>
  /** A project's step that jobs have reached, or one before it: it keeps its
   *  place and its step. From the server; never sent back. */
  locked?: boolean
  /** How many jobs reached this step. */
  reached_jobs?: number
  /** The editor's row key, stable across edits and moves. Never sent. */
  _rk?: string
}

// Cap kept single-digit so the row-number gutter only ever holds one digit +
// a dot. Enforced on the backend too (stepdef.MaxWorkflowSteps).
const MAX_STEPS = 9

const PLACEHOLDER: WorkflowStep = { step_definition_id: null, name: '', step_type: 'human' }

/** Rows the user has not finished: a placeholder with no library pick. */
export function unpickedRows(steps: WorkflowStep[]): number[] {
  return steps.flatMap((s, i) => (s.step_definition_id == null && s.id == null ? [i + 1] : []))
}

/** How many leading rows are locked. */
function lockedCount(steps: WorkflowStep[]): number {
  const i = steps.findIndex(s => !s.locked)
  return i < 0 ? steps.length : i
}

/** Moves a row within the unlocked tail; `from` and `to` count from its first
 *  row, so no row can land above a locked one. */
export function moveTailRow<T extends WorkflowStep>(steps: T[], from: number, to: number): T[] {
  const locked = lockedCount(steps)
  return arrayMove(steps, locked + from, locked + to)
}

let rowKeySeq = 0
const newRowKey = () => `ws-${++rowKeySeq}`
// By identity, so it holds no row after its list is gone.
const rowKeys = new WeakMap<WorkflowStep, string>()

/** Gives every row a `_rk`, so a re-render does not remount it (and close a
 *  picker open in it). A saved row is keyed by its id, which survives a
 *  refetch; an unsaved one keeps the key it got the first time, by identity. */
function useKeyedRows(steps: WorkflowStep[]): WorkflowStep[] {
  return useMemo(() => steps.map(s => {
    if (s._rk) return s
    if (s.id != null) return { ...s, _rk: `step-${s.id}` }
    let k = rowKeys.get(s)
    if (!k) {
      k = newRowKey()
      rowKeys.set(s, k)
    }
    return { ...s, _rk: k }
  }), [steps])
}

interface Props {
  steps: WorkflowStep[]
  onChange: (steps: WorkflowStep[]) => void
  /** Project source language. When undefined (library context) the assignee
   *  picker skips the language-pair filter and shows all members. */
  sourceLang?: string
  /** Project target languages. Empty array OR undefined disables the filter. */
  targetLangs?: string[]
  /** A project's steps: each row gets its deadline column. */
  deadlines?: boolean
}

export function WorkflowStepsEditor({ steps: rawSteps, onChange, sourceLang, targetLangs, deadlines = false }: Props) {
  // Composing a workflow is when you notice the step you want does not exist.
  // Pinned in the picker, opening the step library's create form in a new tab
  // — same machinery as every other "New …" entry, including the refresh that
  // makes the new step show up here on the way back.
  const { stepDefinition } = useEngineCreateActions()
  const newStepActions = useMemo(() => [stepDefinition()], [stepDefinition])
  const steps = useKeyedRows(rawSteps)
  const langs = useStepLanguageDisclosure()

  // ── Library + picker data ────────────────────────────────────────────────
  const { data: definitions } = useQuery({ queryKey: queryKeys.stepDefinitions.all, queryFn: listStepDefinitions })
  const { data: members } = useQuery({ queryKey: queryKeys.members.all, queryFn: listMembers })
  const { data: routers } = useQuery({ queryKey: queryKeys.aiRouters.all, queryFn: listAIRouters })

  const hasProjectContext = !!(sourceLang && targetLangs && targetLangs.length > 0)
  const pairQueries = useQueries({
    queries: hasProjectContext
      ? targetLangs!.map(target => ({
          queryKey: queryKeys.usersByPair.lookup(sourceLang!, target),
          queryFn: () => listUsersByLanguagePair(sourceLang!, target),
          enabled: hasProjectContext,
        }))
      : [],
  })

  // Library context: all members. Project context: managers OR anyone
  // qualified for ANY of the project's target pairs (a step's default
  // assignee is one user across all the project's jobs).
  const assignees: Member[] = useMemo(() => {
    const all = members ?? []
    if (!hasProjectContext) return all
    const qualifiedIds = new Set<number>()
    for (const q of pairQueries) {
      if (q.data) for (const u of q.data) qualifiedIds.add(u.id)
    }
    return all.filter(m => isManagerRole(m.role) || qualifiedIds.has(m.id))
  }, [members, pairQueries, hasProjectContext])

  const stepOptions = useMemo(
    // `step_type` is a plain string on StepDefinition (tygo mirrors the Go
    // column), so an unrecognised value reads as a human step.
    () => (definitions ?? []).map(d => {
      const StepTypeChip = STEP_TYPE_CHIPS[d.step_type as StepTypePresetKey] ?? HumanStepChip
      return { value: String(d.id), label: d.name, chip: <StepTypeChip /> }
    }),
    [definitions],
  )
  const libraryEmpty = definitions !== undefined && definitions.length === 0

  const pickDefinition = (idx: number, value: string) => {
    const def = definitions?.find(d => String(d.id) === value)
    onChange(steps.map((s, i) => {
      if (i !== idx) return s
      if (!def) return { ...s, step_definition_id: null }
      return {
        ...s,
        step_definition_id: def.id,
        name: def.name,
        step_type: def.step_type as StepType,
        ai_router_id: def.ai_router_id,
        // An automated step has nobody on it, and no deadline.
        ...(isAutomatedStep(def.step_type) ? { default_assignee: undefined, deadline: null, language_deadlines: {} } : {}),
      }
    }))
  }

  const locked = lockedCount(steps)
  const tail = steps.slice(locked)
  const row = (idx: number, grip?: ReactNode) => (
    <WorkflowStepRow
      steps={steps}
      index={idx}
      onChange={onChange}
      stepOptions={stepOptions}
      newStepActions={newStepActions}
      onPickDefinition={pickDefinition}
      assignees={assignees}
      members={members}
      routers={routers}
      deadlines={deadlines ? { targetLanguages: targetLangs ?? [], open: langs.isOpen(idx), onToggle: () => langs.toggle(idx) } : undefined}
      grip={grip}
    />
  )

  return (
    <div className="space-y-2">
      {libraryEmpty && (
        <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="workflow-library-empty">
          The step library is empty. <Link to="/workflows/steps" className="underline">Create a step</Link> first — steps are defined once and reused.
        </p>
      )}
      {steps.slice(0, locked).map((s, idx) => <div key={s._rk}>{row(idx)}</div>)}
      {/* Its own box, so a dragged row stays below the locked ones. */}
      <div className="space-y-2">
        <SortableList ids={tail.map(s => s._rk!)} onMove={(from, to) => onChange(moveTailRow(steps, from, to))}>
          {tail.map((s, i) => (
            <SortableGridRow key={s._rk} id={s._rk!} disabled={false} gridCols="minmax(0,1fr)" testId={`workflow-step-sortable-${locked + i}`} className="rounded">
              {({ handleRef, handleProps }) => row(locked + i, (
                <DragHandle ref={handleRef} {...handleProps} data-testid={`workflow-step-drag-${locked + i}`} />
              ))}
            </SortableGridRow>
          ))}
        </SortableList>
      </div>
      {/* Full-width dashed add-row spanning the step rows, clamped to the
          `h-input` height of the row controls (`--spacing-input`) rather than
          the list-row default. Hidden at the cap (MAX_STEPS) so numbering
          stays single-digit. */}
      {steps.length >= MAX_STEPS ? (
        <p className="py-1.5 text-center text-xs text-gray-400 dark:text-gray-500">
          Maximum of {MAX_STEPS} steps reached.
        </p>
      ) : (
        <AddItemButton
          label="Add step"
          onClick={() => onChange([...steps, { ...PLACEHOLDER, _rk: newRowKey() }])}
          testId="workflow-add-step"
          className="min-h-(--spacing-input) rounded"
        />
      )}
    </div>
  )
}
