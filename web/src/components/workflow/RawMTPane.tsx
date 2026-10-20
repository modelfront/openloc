// The Workflow card under Raw MT: the built-in Pre-translation workflow, shown
// as the rail's Pre-translation row and nothing else — no dropdown. There is
// nothing to pick; steps after pre-translation are a Workflow.
import type { ReactNode } from 'react'
import { TextLink } from '@/components/ui/text-link'
import { STEP_TYPE_PRESETS } from '@/components/ui/chip-presets'
import { drawAttention } from '@/lib/attention'
import { PickerHeading } from './WorkflowPicker'
import { WorkflowStepChips } from './WorkflowStepChips'

/** Takes the user to the Machine translation card and its router picker. */
export const focusMTRouter = () =>
  drawAttention({ target: '[data-testid="mt-section"]', focus: '[data-testid="mt-router-select"]' })

/** The MT router's name on the Pre-translation row, a link to where it is
 *  picked. Default: the Machine translation card on this page. */
export function MTRouterLink({ mtRouterName, onPickRouter = focusMTRouter }: { mtRouterName?: string | null; onPickRouter?: () => void }) {
  return (
    <TextLink
      tone={STEP_TYPE_PRESETS.pretranslate.color}
      onClick={onPickRouter}
      title="MT router picks the engine for each job's target language"
      data-testid="raw-mt-router-link"
    >
      {mtRouterName ?? 'No MT router yet'}
    </TextLink>
  )
}

interface Props {
  /** The MT router, shown on the Pre-translation row. */
  mtRouterName?: string | null
  /** Takes the user to the MT router picker. Default: the card on this page. */
  onPickRouter?: () => void
  /** The mode toggle. */
  heading: ReactNode
}

export function RawMTPane({ mtRouterName, onPickRouter, heading }: Props) {
  return (
    <div data-testid="raw-mt-pane" className="space-y-4">
      <PickerHeading
        heading={heading}
        help="Machine translation fills in each file when it is added, and the job completes by itself; nobody is assigned. To run anything after that, switch to Workflow."
      />
      <WorkflowStepChips
        steps={[]}
        data-testid="raw-mt-pretranslation"
        pretranslation={
          <>
            <MTRouterLink mtRouterName={mtRouterName} onPickRouter={onPickRouter} />
            <span className="text-gray-500">→ Automated</span>
          </>
        }
      />
    </div>
  )
}
