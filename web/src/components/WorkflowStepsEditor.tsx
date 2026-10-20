// WorkflowStepsEditor — the ordered step list of a project, template, or
// library workflow.
//
// Steps are picked from the step library, never typed: a row's identity
// (name, type, engine) is the definition's; the editor owns order and the
// default assignee. A pre-library row whose name is not in the library keeps
// its identity through its own id and shows as "(custom)" until swapped.
// Assignee picker filtering:
//   - PROJECT context (sourceLang + targetLangs): users qualified for AT LEAST
//     ONE of the project's target pairs, UNION admins/owners (always).
//   - TEMPLATE / LIBRARY context (no language props): ALL active members.
// An `ai` step has nobody on it, so its assignee cell reads "Automated".

import { useState, useEffect, useRef, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useEngineCreateActions } from '../hooks/useEngineCreateActions'
import { useQuery, useQueries } from '@tanstack/react-query'
import { toast } from 'sonner'
import { listMembers, listUsersByLanguagePair, listStepDefinitions, type Member, type StepType } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isManagerRole } from '../lib/roles'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Combobox } from '@/components/ui/combobox'
import { AddItemButton } from '@/components/list'
import { DragHandle } from '@/components/ui/drag-handle'
import { STEP_TYPE_CHIPS, HumanStepChip, type StepTypePresetKey } from '@/components/ui/chip-presets'
import { isAutomatedStep } from '@/lib/workflow-steps'
import { WorkflowStepChips } from './workflow/WorkflowStepChips'
import { errorText } from '@/lib/error-text'

export interface WorkflowStep {
  /** Existing profile row id (edit mode). A row without a library reference
   *  is carried forward through this id. */
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
}

// Cap kept single-digit so the row-number gutter only ever holds one digit +
// a dot. Enforced on the backend too (stepdef.MaxWorkflowSteps).
const MAX_STEPS = 9

const PLACEHOLDER: WorkflowStep = { step_definition_id: null, name: '', step_type: 'human' }

// A stable per-row key that survives reorder and delete, so React keeps each
// controlled Combobox attached to its logical row
// instead of reconciling by array position. Not part of the API shape.
type DraftStep = WorkflowStep & { _rk: string }
let rowKeySeq = 0
const withKey = (s: WorkflowStep): DraftStep => ({ ...s, _rk: `ws-${++rowKeySeq}` })
const toDrafts = (steps: WorkflowStep[]): DraftStep[] => steps.map(withKey)

interface Props {
  steps: WorkflowStep[]
  onSave?: (steps: WorkflowStep[]) => Promise<void>
  initialEditing?: boolean
  onDraftChange?: (steps: WorkflowStep[]) => void
  /** Project source language. When undefined (template / library context) the
   *  assignee picker skips the language-pair filter and shows all members. */
  sourceLang?: string
  /** Project target languages. Empty array OR undefined disables the filter. */
  targetLangs?: string[]
  /** View mode only, no Edit button — the steps belong to an attached library
   *  workflow and are unlocked by detaching. */
  readOnly?: boolean
}

/** Rows the user has not finished: a placeholder with no library pick. */
export function unpickedRows(steps: WorkflowStep[]): number[] {
  return steps.flatMap((s, i) => (s.step_definition_id == null && s.id == null ? [i + 1] : []))
}

export function WorkflowStepsEditor({
  steps: initialSteps,
  onSave,
  initialEditing,
  onDraftChange,
  sourceLang,
  targetLangs,
  readOnly = false,
}: Props) {
  // Composing a workflow is when you notice the step you want does not exist.
  // Pinned in the picker, opening the step library's create form in a new tab
  // — same machinery as every other "New …" entry, including the refresh that
  // makes the new step show up here on the way back.
  const { stepDefinition } = useEngineCreateActions()
  const newStepActions = useMemo(() => [stepDefinition()], [stepDefinition])

  const [editing, setEditing] = useState(initialEditing ?? false)
  const [draft, setDraft] = useState<DraftStep[]>(initialEditing ? toDrafts(initialSteps) : [])
  const [saving, setSaving] = useState(false)
  const prevStepsRef = useRef(initialSteps)

  // Drag-to-reorder (mirrors AssignmentSection): native HTML5 DnD, handle-armed.
  // Step order IS array position (persisted as step_order on save).
  const [dragIdx, setDragIdx] = useState<number | null>(null)
  const [dragArmed, setDragArmed] = useState(false)

  const reorder = (from: number, to: number) => {
    setDraft(prev => {
      if (from === to || from < 0 || to < 0 || from >= prev.length || to >= prev.length) return prev
      const next = [...prev]
      const [moved] = next.splice(from, 1)
      next.splice(to, 0, moved)
      return next
    })
  }

  // Sync items when they change externally (e.g. template / workflow applied).
  useEffect(() => {
    if (initialEditing && initialSteps !== prevStepsRef.current) {
      prevStepsRef.current = initialSteps
      setDraft(toDrafts(initialSteps))
    }
  }, [initialSteps, initialEditing])

  // Report draft changes to parent (create mode).
  useEffect(() => {
    if (initialEditing && onDraftChange) {
      onDraftChange(draft)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft])

  // ── Library + picker data ────────────────────────────────────────────────
  const { data: definitions } = useQuery({ queryKey: queryKeys.stepDefinitions.all, queryFn: listStepDefinitions })
  const { data: members } = useQuery({ queryKey: queryKeys.members.all, queryFn: listMembers })

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

  // Template / library context: all members. Project context: managers OR
  // anyone qualified for ANY of the project's target pairs (a step's default
  // assignee is one user across all the project's jobs).
  const pickerOptions: Member[] = useMemo(() => {
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

  const showEditControls = !initialEditing && !readOnly

  const startEdit = () => {
    setDraft(toDrafts(initialSteps))
    setEditing(true)
  }

  const handleSave = async () => {
    if (!onSave) return
    const unpicked = unpickedRows(draft)
    if (unpicked.length > 0) {
      toast.error(`Pick a step for row ${unpicked.join(', ')} or remove it`)
      return
    }
    setSaving(true)
    try {
      await onSave(draft)
      setEditing(false)
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
    } finally {
      setSaving(false)
    }
  }

  const pickDefinition = (idx: number, value: string) => {
    const def = definitions?.find(d => String(d.id) === value)
    setDraft(prev => prev.map((s, i) => {
      if (i !== idx) return s
      if (!def) return { ...s, step_definition_id: null }
      return {
        ...s,
        step_definition_id: def.id,
        name: def.name,
        step_type: def.step_type as StepType,
        ai_router_id: def.ai_router_id,
        // An automated step has nobody on it.
        default_assignee: isAutomatedStep(def.step_type) ? undefined : s.default_assignee,
      }
    }))
  }

  if (!editing) {
    return (
      <div>
        {initialSteps.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-gray-500">No workflow steps configured.</p>
        ) : (
          <WorkflowStepChips
            steps={initialSteps}
          />
        )}
        {showEditControls && (
          <Button size="sm" variant="outline" className="mt-2" onClick={startEdit}>Edit</Button>
        )}
      </div>
    )
  }

  // A full literal, never assembled: the Tailwind scanner only emits classes
  // it can read verbatim from the source, so an interpolated `grid-cols-[…]`
  // produces no columns and the row stacks. Fixed tracks, never `auto`: each
  // row is its own grid, so a content-sized track would drift row to row.
  const gridCols = 'grid-cols-[1.25rem_1.5rem_minmax(12rem,1.3fr)_minmax(8rem,1fr)_1.75rem]'

  return (
    <div className="space-y-2">
      {libraryEmpty && (
        <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="workflow-library-empty">
          The step library is empty. <Link to="/workflows/steps" className="underline">Create a step</Link> first — steps are defined once and reused.
        </p>
      )}
      {draft.map((step, idx) => {
        const isLegacy = step.step_definition_id == null && step.id != null
        const pickerValue = step.step_definition_id != null ? String(step.step_definition_id) : isLegacy ? `legacy:${step.id}` : ''
        const LegacyStepTypeChip = STEP_TYPE_CHIPS[step.step_type]
        const options = isLegacy
          ? [{ value: `legacy:${step.id}`, label: `${step.name} (custom)`, chip: <LegacyStepTypeChip /> }, ...stepOptions]
          : stepOptions
        const assigneeID = step.default_assignee
        const isStaleAssignee = assigneeID != null && members !== undefined && !pickerOptions.some(m => m.id === assigneeID)
        return (
          <div
            key={step._rk}
            className={dragIdx === idx ? 'opacity-50' : undefined}
            draggable={dragArmed}
            onDragStart={(e) => { e.stopPropagation(); setDragIdx(idx); e.dataTransfer.effectAllowed = 'move' }}
            onDragEnter={(e) => { e.preventDefault(); e.stopPropagation(); if (dragIdx === null || dragIdx === idx) return; reorder(dragIdx, idx); setDragIdx(idx) }}
            onDragOver={(e) => { e.preventDefault(); e.stopPropagation() }}
            onDrop={(e) => { e.preventDefault(); e.stopPropagation() }}
            onDragEnd={(e) => { e.stopPropagation(); setDragIdx(null); setDragArmed(false) }}
          >
            <div
              className={`grid ${gridCols} items-center gap-2 text-sm`}
              data-testid={`workflow-step-row-${idx}`}
            >
            <DragHandle
              onMouseDown={() => setDragArmed(true)}
              onMouseUp={() => setDragArmed(false)}
              data-testid={`workflow-step-drag-${idx}`}
            />
            <span className="text-gray-400 tabular-nums shrink-0">{idx + 1}.</span>
            <Combobox
              value={pickerValue}
              onChange={v => { if (!v.startsWith('legacy:')) pickDefinition(idx, v) }}
              options={options}
              actions={newStepActions}
              placeholder="Choose a step…"
              searchPlaceholder="Search steps…"
              emptyText="No step with that name — create it in the step library."
              className={`min-w-0 w-full ${pickerValue === '' ? 'border-amber-400 dark:border-amber-700' : ''}`}
              contentClassName="w-80"
              aria-label="Workflow step"
              data-testid={`workflow-step-picker-${idx}`}
            />
            {isAutomatedStep(step.step_type) ? (
              <span className="text-xs text-gray-500 dark:text-gray-400 px-2" title="An automated step completes itself — nobody is assigned" data-testid={`workflow-step-automated-${idx}`}>
                Automated
              </span>
            ) : (
              <Combobox
                value={assigneeID != null ? String(assigneeID) : ''}
                onChange={v => setDraft(prev => prev.map((s, i) => i === idx
                  ? { ...s, default_assignee: v === '' ? undefined : Number(v) }
                  : s))}
                options={[
                  { value: '', label: 'No default' },
                  ...(isStaleAssignee ? [{ value: String(assigneeID), label: '(deleted user — pick replacement)' }] : []),
                  ...pickerOptions.map(m => ({ value: String(m.id), label: m.name })),
                ]}
                data-testid={`workflow-step-assignee-${idx}`}
                className={`min-w-0 w-full ${isStaleAssignee ? 'border-rose-300 dark:border-rose-800 text-rose-700 dark:text-rose-300' : ''}`}
                aria-label="Default assignee"
              />
            )}
            <DeleteButton
              aria-label="Remove step"
              onClick={() => setDraft(prev => prev.filter((_, i) => i !== idx))}
              data-testid={`workflow-step-remove-${idx}`}
            />
            </div>
          </div>
        )
      })}
      {/* Full-width dashed add-row spanning the step rows, clamped to the
          `h-input` height of the row controls (`--spacing-input`) rather than
          the list-row default. Hidden at the cap (MAX_STEPS) so numbering
          stays single-digit. */}
      {draft.length >= MAX_STEPS ? (
        <p className="py-1.5 text-center text-xs text-gray-400 dark:text-gray-500">
          Maximum of {MAX_STEPS} steps reached.
        </p>
      ) : (
        <AddItemButton
          label="Add step"
          onClick={() => setDraft(prev => [...prev, withKey(PLACEHOLDER)])}
          testId="workflow-add-step"
          className="min-h-(--spacing-input) rounded"
        />
      )}
      {showEditControls && (
        <div className="flex gap-2 pt-1">
          <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>
          <Button size="sm" onClick={handleSave} disabled={saving}>Save</Button>
        </div>
      )}
    </div>
  )
}
