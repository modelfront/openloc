// One row of WorkflowStepsEditor: grip, number, step picker, default
// assignee, the deadline column on a project, and the trailing delete.
//
// A locked row is one jobs have reached (or one before it): its grip is a lock
// that says why, its step cannot be swapped and it has no delete, but its
// assignee and deadline stay editable — they decide who and when, not what.
import type { ReactNode } from 'react'
import { Lock } from 'lucide-react'
import { Combobox, type ComboboxAction } from '@/components/ui/combobox'
import { DeleteButton } from '@/components/ui/delete-button'
import { SectionInfo } from '@/components/ui/section-info'
import { STEP_TYPE_CHIPS } from '@/components/ui/chip-presets'
import type { AIRouter, Member } from '@/api'
import { isAutomatedStep, runsAIStep } from '@/lib/workflow-steps'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { StepDeadlineColumn } from './WorkflowDeadlines'
import { StepRouterNote } from './StepRouterNote'

export interface StepOption {
  value: string
  label: string
  chip: ReactNode
}

/** The deadline column's inputs; absent outside a project. */
export interface RowDeadlines {
  targetLanguages: string[]
  open: boolean
  onToggle: () => void
}

interface Props {
  steps: WorkflowStep[]
  index: number
  onChange: (steps: WorkflowStep[]) => void
  stepOptions: StepOption[]
  newStepActions: ComboboxAction[]
  onPickDefinition: (index: number, value: string) => void
  /** Who the assignee picker offers (the project's language-pair filter). */
  assignees: Member[]
  /** Every member, to name the current pick; undefined while loading, so no
   *  assignee reads as deleted yet. */
  members: Member[] | undefined
  /** For an AI step's router line; undefined while loading. */
  routers: AIRouter[] | undefined
  deadlines?: RowDeadlines
  /** The drag grip, for a row in the sortable part of the list. */
  grip?: ReactNode
}

// A full literal, never assembled: the Tailwind scanner only emits classes it
// can read verbatim from the source. Fixed tracks, never `auto`: each row is
// its own grid, so a content-sized track would drift row to row.
const GRID_COLS = 'grid-cols-[1.5rem_1.5rem_minmax(12rem,1.3fr)_minmax(8rem,1fr)]'

function lockReason(step: WorkflowStep): string {
  const n = step.reached_jobs ?? 0
  return n > 0
    ? `${n} ${n === 1 ? 'job has' : 'jobs have'} reached this step, so it keeps its place. You can still change its assignee and deadline.`
    : 'Jobs have reached a later step, so this one keeps its place. You can still change its assignee and deadline.'
}

export function WorkflowStepRow({
  steps, index, onChange, stepOptions, newStepActions, onPickDefinition, assignees, members, routers, deadlines, grip,
}: Props) {
  const step = steps[index]
  const locked = !!step.locked
  const isLegacy = step.step_definition_id == null && step.id != null
  const pickerValue = step.step_definition_id != null ? String(step.step_definition_id) : isLegacy ? `legacy:${step.id}` : ''
  const LegacyStepTypeChip = STEP_TYPE_CHIPS[step.step_type]
  const options = isLegacy
    ? [{ value: `legacy:${step.id}`, label: `${step.name} (custom)`, chip: <LegacyStepTypeChip /> }, ...stepOptions]
    : stepOptions
  const assigneeID = step.default_assignee
  const current = assigneeID != null ? members?.find(m => m.id === assigneeID) : undefined
  // Deleted: no longer a member. A member the filter does not offer stays
  // picked under their own name.
  const isStaleAssignee = assigneeID != null && members !== undefined && !current
  const currentOutsideFilter = current && !assignees.some(m => m.id === current.id)
  const patch = (change: Partial<WorkflowStep>) => onChange(steps.map((s, i) => (i === index ? { ...s, ...change } : s)))

  return (
    <div className="space-y-1 text-sm" data-testid={`workflow-step-row-${index}`} data-locked={locked ? 'true' : undefined}>
      <div className="flex items-start gap-2">
        <div className={`grid ${GRID_COLS} min-w-0 flex-1 items-center gap-2`}>
          <span className="flex justify-center">
            {locked ? (
              <SectionInfo icon={Lock} label="Locked step" data-testid={`workflow-step-locked-${index}`}>
                {lockReason(step)}
              </SectionInfo>
            ) : grip}
          </span>
          <span className="text-gray-400 tabular-nums">{index + 1}.</span>
          <Combobox
            value={pickerValue}
            onChange={v => { if (!v.startsWith('legacy:')) onPickDefinition(index, v) }}
            options={options}
            actions={newStepActions}
            disabled={locked}
            placeholder="Choose a step…"
            searchPlaceholder="Search steps…"
            emptyText="No step with that name — create it in the step library."
            aria-invalid={pickerValue === ''}
            className="min-w-0 w-full"
            contentClassName="w-80"
            aria-label="Workflow step"
            data-testid={`workflow-step-picker-${index}`}
          />
          {isAutomatedStep(step.step_type) ? (
            <span className="px-2 text-xs text-gray-500 dark:text-gray-400" title="An automated step completes itself — nobody is assigned" data-testid={`workflow-step-automated-${index}`}>
              Automated
            </span>
          ) : (
            <Combobox
              value={assigneeID != null ? String(assigneeID) : ''}
              onChange={v => patch({ default_assignee: v === '' ? undefined : Number(v) })}
              options={[
                { value: '', label: 'No default' },
                ...(isStaleAssignee ? [{ value: String(assigneeID), label: '(deleted user — pick replacement)' }] : []),
                ...(currentOutsideFilter ? [{ value: String(current.id), label: current.name }] : []),
                ...assignees.map(m => ({ value: String(m.id), label: m.name })),
              ]}
              data-testid={`workflow-step-assignee-${index}`}
              aria-invalid={isStaleAssignee}
              className="min-w-0 w-full"
              aria-label="Default assignee"
            />
          )}
        </div>
        {deadlines && (
          // Holds the column's place on an automated step, which has none, so
          // the deletes stay in one column.
          <div className="flex min-h-input min-w-40 items-center justify-end">
            <StepDeadlineColumn
              steps={steps}
              index={index}
              targetLanguages={deadlines.targetLanguages}
              onChange={onChange}
              open={deadlines.open}
              onToggle={deadlines.onToggle}
            />
          </div>
        )}
        <span className="flex h-input w-7 shrink-0 items-center justify-center">
          {!locked && (
            <DeleteButton
              aria-label="Remove step"
              onClick={() => onChange(steps.filter((_, i) => i !== index))}
              data-testid={`workflow-step-remove-${index}`}
            />
          )}
        </span>
      </div>
      {/* Under the picker: which router the AI step runs, and what is wrong with it. */}
      {runsAIStep(step.step_type) && (
        <div className="flex flex-wrap items-center gap-x-2 pl-14 text-xs" data-testid={`workflow-step-router-${index}`}>
          <StepRouterNote step={step} routers={routers} />
        </div>
      )}
    </div>
  )
}
