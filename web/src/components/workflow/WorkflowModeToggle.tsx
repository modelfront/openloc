// The Workflow card's mode: Workflow — a library workflow — or Raw MT — the
// built-in Pre-translation workflow. The mode is read off the steps, so a
// template or a copied project that carries that workflow shows Raw MT.
// Choosing Raw MT applies it; choosing Workflow goes back to the library
// workflow left for it, else the first one in the library.
import { useState, type RefObject } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Workflow as WorkflowIcon } from 'lucide-react'
import { getPretranslateWorkflow, type WorkflowDetail, type WorkflowListItem } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { workflowMode, type WorkflowMode } from '../../lib/workflow-steps'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { STEP_TYPE_PRESETS } from '@/components/ui/chip-presets'
import ConfirmDialog from '../ConfirmDialog'

interface Props {
  steps: { step_type?: string | null }[]
  /** The library workflow the steps came from (null when none). */
  workflowId: number | null
  /** The Workflows library, for the way back. */
  workflows: WorkflowListItem[]
  /** Where the workflow left for Raw MT is remembered. Owned by the card: the
   *  toggle itself remounts when the pane under it changes. */
  previousWorkflowRef: RefObject<number | null>
  /** Applies a workflow. The built-in one arrives with its detail, which the
   *  library's GET does not serve. */
  onApply: (id: number, detail?: WorkflowDetail) => Promise<unknown>
  /** Ask first: the steps of a template may be in use. */
  confirmApply?: boolean
  /** Extra sentence in the confirmation. */
  applyWarning?: string
}

/** The built-in Pre-translation workflow, which the library's GET does not serve. */
function usePretranslateWorkflow() {
  return useQuery({ queryKey: queryKeys.workflows.pretranslateOnly, queryFn: getPretranslateWorkflow })
}

const PretranslateIcon = STEP_TYPE_PRESETS.pretranslate.Icon
export const PretranslateWorkflowIcon = () => <PretranslateIcon className="size-3.5 text-primary" aria-hidden />

export function WorkflowModeToggle({ steps, workflowId, workflows, previousWorkflowRef, onApply, confirmApply = false, applyWarning }: Props) {
  const { data: builtIn } = usePretranslateWorkflow()
  const mode = workflowMode(steps)
  const [pending, setPending] = useState<WorkflowMode | null>(null)
  const [busy, setBusy] = useState(false)

  const backTo = workflows.find(w => w.id === previousWorkflowRef.current) ?? workflows[0]

  const apply = async (next: WorkflowMode) => {
    setBusy(true)
    try {
      if (next === 'raw_mt' && builtIn) {
        previousWorkflowRef.current = workflowId
        await onApply(builtIn.id, builtIn)
      } else if (next === 'workflow' && backTo) {
        await onApply(backTo.id)
      }
    } finally {
      setBusy(false)
    }
  }

  const handleChange = (next: WorkflowMode) => {
    if (next === mode) return
    if (confirmApply) setPending(next)
    else void apply(next)
  }

  // Only the option not chosen can be unavailable: the chosen one stays lit.
  const cannot = (target: WorkflowMode) => (target === 'raw_mt' ? !builtIn : !backTo)
  const unavailable = (target: WorkflowMode) => target !== mode && (busy || cannot(target))

  return (
    <>
      <ChoiceToggle
        value={mode}
        onChange={handleChange}
        aria-label="Workflow mode"
        tone="primary"
        data-testid="workflow-mode"
        options={[
          { value: 'workflow', label: 'Workflow', caption: 'Human and AI steps', icon: WorkflowIcon, disabled: unavailable('workflow') },
          { value: 'raw_mt', label: 'Raw MT', caption: 'Pre-translation', icon: PretranslateIcon, disabled: unavailable('raw_mt') },
        ]}
      />
      <ConfirmDialog
        open={pending != null}
        onOpenChange={open => !open && setPending(null)}
        title={pending === 'raw_mt' ? 'Use Raw MT?' : `Use workflow “${backTo?.name ?? ''}”?`}
        description={`${pending === 'raw_mt'
          ? 'This replaces the current steps with the built-in Pre-translation step and turns on machine translation on upload.'
          : "This replaces the current steps with a copy of the workflow's steps."}${applyWarning ? ` ${applyWarning}` : ''}`}
        actionLabel={pending === 'raw_mt' ? 'Use Raw MT' : 'Use workflow'}
        variant="default"
        onConfirm={() => {
          const next = pending
          setPending(null)
          if (next) void apply(next)
        }}
      />
    </>
  )
}
