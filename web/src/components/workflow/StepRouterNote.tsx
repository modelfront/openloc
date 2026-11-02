// The AI router of a hybrid / ai step, as the step lists show it: the
// router's name in the step type's colour, linking to its card on the AI
// page, then what is wrong with it, if anything. Nothing for a human step.
import type { AIRouter } from '@/api'
import { cn } from '@/lib/utils'
import { attentionHref } from '@/lib/attention'
import { TextLink } from '@/components/ui/text-link'
import { chipTextClass } from '@/components/ui/chip'
import { ROUTER_ENGINE_PRESETS, STEP_TYPE_PRESETS, type StepTypePresetKey } from '@/components/ui/chip-presets'
import { runsAIStep } from '@/lib/workflow-steps'

export interface RouterStep {
  step_type: string
  ai_router_id?: number | null
  /** Pre-resolved by the list endpoint; else looked up in `routers`. */
  ai_router_name?: string | null
}

export function StepRouterNote({ step, routers }: { step: RouterStep; routers: AIRouter[] | undefined }) {
  const router = step.ai_router_id != null ? routers?.find(r => r.id === step.ai_router_id) : undefined
  const routerName = step.ai_router_name ?? router?.name ?? null
  // A hybrid/ai step with no router anywhere: deleted since it was set.
  const routerDeleted = runsAIStep(step.step_type) && (
    step.ai_router_id == null || (step.ai_router_name === undefined && routers !== undefined && !router)
  )
  const typeKey = (step.step_type in STEP_TYPE_PRESETS ? step.step_type : 'human') as StepTypePresetKey
  return (
    <>
      {/* Opens the AI page pointing at this router's card. */}
      {routerName && (
        <TextLink
          to={step.ai_router_id != null ? attentionHref('/ai', `ai-router-${step.ai_router_id}`) : '/ai'}
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
    </>
  )
}
