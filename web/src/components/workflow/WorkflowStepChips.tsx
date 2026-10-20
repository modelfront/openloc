// A workflow's steps wherever they are shown read-only — the Workflows list,
// the project / template Workflow tab, the wizard — as a vertical stepper:
// numbered dots on a rail, then the name, then a second column that opens with
// the step type's icon (who acts) and carries the AI router (in the type's
// colour) and who picks the step up (or "Automated"). Pre-translation, when it
// runs, heads the rail the same way: the MT icon in its dot instead of a
// number, since it happens before step 1, and the AI icon before its MT
// router — MT is AI too. One component, so the library and a project read the
// same. With `selection`, each step is also a radio, for a dialog that asks
// "which step?" (Reject's target).
import { useId, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listAIRouters, listMembers } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { cn } from '@/lib/utils'
import { attentionHref } from '@/lib/attention'
import { TextLink } from '@/components/ui/text-link'
import { EditableTitle } from '@/components/list/EditableTitle'
import { chipTextClass } from '@/components/ui/chip'
import {
  AIStepChip,
  ROUTER_ENGINE_PRESETS,
  STEP_TYPE_CHIPS,
  STEP_TYPE_PRESETS,
  type StepTypePresetKey,
} from '@/components/ui/chip-presets'
import { isAutomatedStep, runsAIStep } from '@/lib/workflow-steps'

/** The fields a step needs to render as a chip. Names may come pre-resolved
 *  (the list endpoint joins them) or be looked up here from the ids. */
export interface ChipStep {
  name: string
  step_type: string
  /** null = a pre-library ("custom") row; undefined = not applicable. */
  step_definition_id?: number | null
  ai_router_id?: number | null
  ai_router_name?: string | null
  default_assignee?: number | null
  assignee_name?: string | null
}

const PretranslateIcon = STEP_TYPE_PRESETS.pretranslate.Icon

/** Makes each step a radio: one step picked, some not pickable. */
export interface StepSelection {
  /** The picked step's index, or null. */
  value: number | null
  onChange: (index: number) => void
  /** Why a step cannot be picked, shown on hover; undefined = pickable. */
  blockReason?: (index: number) => string | undefined
  'aria-label': string
}

export function WorkflowStepChips({ steps, className, trailing, pretranslation, selection, 'data-testid': testId }: {
  steps: ChipStep[]
  /** Merged onto the container (spacing against the caller's layout). */
  className?: string
  /** Rendered at the right end of a step's row — the project screens put the
   *  step's deadline column there. Return null for a step that should show none. */
  trailing?: (index: number) => ReactNode
  /** Heads the rail with the Pre-translation row; what is given follows its
   *  name (the MT router link, "→ Automated"). */
  pretranslation?: ReactNode
  selection?: StepSelection
  'data-testid'?: string
}) {
  const radioName = useId()
  const { data: routers } = useQuery({ queryKey: queryKeys.aiRouters.all, queryFn: listAIRouters })
  const { data: members } = useQuery({ queryKey: queryKeys.members.all, queryFn: listMembers })
  const rows = steps.length + (pretranslation ? 1 : 0)
  return (
    <div
      className={cn('relative', rows > 0 && 'ml-3.5 pl-[26px]', className)}
      data-testid={testId}
      role={selection ? 'radiogroup' : undefined}
      aria-label={selection?.['aria-label']}
    >
      {rows === 0 && <span className="text-xs italic text-gray-400">no steps</span>}
      {rows > 1 && (
        <span aria-hidden className="absolute bottom-4 left-[9px] top-4 w-px bg-gray-300 dark:bg-gray-700" />
      )}
      {/* Two columns — name + type icon, then annotations — so the routers
          and assignees line up down the rail. A long name truncates, full text
          in the tooltip, rather than pushing its row's columns out. */}
      {pretranslation && (
        <div className="relative flex w-full items-center gap-2 py-1.5" data-testid="workflow-pretranslation">
          <span
            aria-hidden
            className="absolute -left-[26px] top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded-full border border-primary bg-card text-primary"
          >
            <PretranslateIcon className="size-3" />
          </span>
          <span className="flex w-full min-w-0 items-center gap-2 text-sm">
            <span className="w-36 shrink-0 truncate font-medium">Pre-translation</span>
            <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
              <AIStepChip iconOnly className="shrink-0 px-1 py-0" />
              {pretranslation}
            </span>
          </span>
        </div>
      )}
      {steps.map((s, i) => {
        const router = s.ai_router_id != null ? routers?.find(r => r.id === s.ai_router_id) : undefined
        const routerName = s.ai_router_name ?? router?.name ?? null
        // A hybrid/ai step with no router anywhere: deleted since it was set.
        const routerDeleted = runsAIStep(s.step_type) && (
          s.ai_router_id == null || (s.ai_router_name === undefined && routers !== undefined && !router)
        )
        const assigneeName = s.assignee_name ?? (s.default_assignee != null ? members?.find(m => m.id === s.default_assignee)?.name : undefined) ?? null
        const assigneeDeleted = s.default_assignee != null && !assigneeName && (s.assignee_name === null || members !== undefined)
        // `step_type` is a free string on the wire, so an unrecognised value
        // reads as a human step. The testid keeps the RAW value, so such a
        // value stays visible to a test asserting on it.
        const typeKey = (s.step_type in STEP_TYPE_PRESETS ? s.step_type : 'human') as StepTypePresetKey
        const StepTypeChip = STEP_TYPE_CHIPS[typeKey]
        const blocked = selection?.blockReason?.(i)
        const Row = selection ? 'label' : 'div'
        return (
          <div key={i} className="flex w-full flex-col gap-1.5">
            {/* The row is full width so `trailing` can sit at its right edge. */}
            <Row
              className={cn('relative flex w-full items-center gap-2 py-1.5', selection && (blocked ? 'opacity-50' : 'cursor-pointer'))}
              title={blocked}
              data-testid={selection ? `workflow-step-option-${i}` : undefined}
              data-disabled={blocked ? true : undefined}
            >
              <span
                aria-hidden
                className="absolute -left-[26px] top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded-full border border-primary bg-card text-[11px] font-medium text-primary"
                data-testid={`workflow-step-dot-${i}`}
              >
                {i + 1}
              </span>
              {selection && (
                <input
                  type="radio"
                  name={radioName}
                  checked={selection.value === i}
                  disabled={!!blocked}
                  onChange={() => selection.onChange(i)}
                  className="shrink-0 accent-primary"
                  data-testid={`workflow-step-pick-${i}`}
                />
              )}
              <span className="flex w-full min-w-0 items-center gap-2 text-sm" data-testid={`workflow-step-badge-${i}`}>
                <span className="flex w-36 shrink-0">
                  <EditableTitle name={s.name} canEdit={false} className="font-medium" />
                </span>
                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                <StepTypeChip iconOnly className="shrink-0 px-1 py-0" data-testid={`step-type-${s.step_type}`} />
                {s.step_definition_id === null && <span className="text-gray-400" title="Not a library step">(custom)</span>}
                {/* Opens the AI page pointing at this router's card. */}
                {routerName && (
                  <TextLink
                    to={s.ai_router_id != null ? attentionHref('/ai', `ai-router-${s.ai_router_id}`) : '/ai'}
                    tone={STEP_TYPE_PRESETS[typeKey].color}
                    title="AI router picks the QE+APE engine for each job's target language. Routers live on the AI page."
                  >
                    {routerName}
                  </TextLink>
                )}
                {routerDeleted && (
                  <span className={cn('italic', chipTextClass('red'))} title="AI router was deleted. Pick another one for this step.">(deleted router)</span>
                )}
                {/* Same hues as the engine chips elsewhere: "something is wrong
                    with what this step points at". */}
                {router?.unusable === 'disabled' && (
                  <span className={cn('italic', chipTextClass('yellow'))} title={ROUTER_ENGINE_PRESETS.disabled.title}>(engine off)</span>
                )}
                {router?.unusable === 'no_key' && (
                  <span className={cn('italic', chipTextClass('red'))} title={ROUTER_ENGINE_PRESETS.no_key.title}>(engine has no key)</span>
                )}
                {isAutomatedStep(s.step_type) ? (
                  <span className="text-gray-500">→ Automated</span>
                ) : assigneeName ? (
                  <span className="text-gray-500">→ {assigneeName}</span>
                ) : assigneeDeleted ? (
                  <span className={cn('italic', chipTextClass('red'))} title="Assignee was deactivated. Edit this step to re-assign.">→ (deleted user)</span>
                ) : null}
                </span>
              </span>
              {trailing?.(i)}
            </Row>
          </div>
        )
      })}
    </div>
  )
}
