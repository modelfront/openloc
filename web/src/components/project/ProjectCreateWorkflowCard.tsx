// The New Project wizard's Workflow card: the mode toggle, then either the
// library dropdown (Workflow) or the Raw MT pane, with the steps read-only
// under it and their deadlines. "New template" builds a workflow from the
// steps on screen.
import { useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { createWorkflow, type WorkflowDetail, type WorkflowListItem } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { workflowDraftsToLibraryInput, workflowMode } from '../../lib/workflow-steps'
import { MTRouterLink, RawMTPane } from '../workflow/RawMTPane'
import { createAction } from '@/components/ui/combobox'
import { useDialogPromise } from '@/hooks/useDialogPromise'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { WorkflowPicker } from '../workflow/WorkflowPicker'
import { WorkflowFormDialog } from '../workflow/WorkflowFormDialog'
import { WorkflowStepsWithDeadlines } from '../workflow/WorkflowDeadlines'
import { WorkflowModeToggle } from '../workflow/WorkflowModeToggle'

interface Props {
  workflows: WorkflowListItem[]
  /** Draft link ('' = steps that came in with a template or duplicated project). */
  workflowId: string
  steps: WorkflowStep[]
  /** Pick a workflow: the caller pre-fills the steps from it. The built-in
   *  one arrives with its detail. */
  onPickWorkflow: (id: string, detail?: WorkflowDetail) => Promise<unknown>
  /** The form's target languages, for the per-language deadlines. */
  targetLanguages: string[]
  /** The form's MT router and whether MT runs on upload. The Pre-translation
   *  row shows only when both are set: without a router nothing runs. */
  mtRouterName?: string
  mtOnUpload?: boolean
  /** Deadline edits, written back onto the step draft. */
  onStepsChange: (steps: WorkflowStep[]) => void
}

export function ProjectCreateWorkflowCard({
  workflows, workflowId, steps, onPickWorkflow, targetLanguages, onStepsChange, mtRouterName, mtOnUpload,
}: Props) {
  const queryClient = useQueryClient()
  const value = workflowId !== '' ? Number(workflowId) : null
  const rawMT = workflowMode(steps) === 'raw_mt'
  const previousWorkflowRef = useRef<number | null>(null)
  const apply = (id: number, detail?: WorkflowDetail) => onPickWorkflow(String(id), detail)

  // The dropdown's create action: opens the dialog, settles with the new id.
  const createDialog = useDialogPromise<number>()

  const heading = (
    <WorkflowModeToggle steps={steps} workflowId={value} workflows={workflows} previousWorkflowRef={previousWorkflowRef} onApply={apply} />
  )

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="create-workflow-card">
      {rawMT ? (
        <RawMTPane mtRouterName={mtRouterName} heading={heading} />
      ) : (
        <WorkflowPicker
          workflows={workflows}
          value={value}
          heading={heading}
          entity="project"
          onApply={async (id) => { await apply(id) }}
          actions={[createAction('New template', createDialog.request, id => { void onPickWorkflow(String(id)) })]}
        >
          {value == null && steps.length > 0 && (
            <p className="mb-1.5 text-xs text-gray-400 dark:text-gray-500">
              Steps carried over from what you started from. Choose a workflow to replace them.
            </p>
          )}
          <WorkflowStepsWithDeadlines
            steps={steps}
            targetLanguages={targetLanguages}
            onChange={onStepsChange}
            pretranslation={mtOnUpload && mtRouterName ? <MTRouterLink mtRouterName={mtRouterName} /> : undefined}
          />
        </WorkflowPicker>
      )}

      <WorkflowFormDialog
        open={createDialog.open}
        onClose={() => createDialog.settle(null)}
        title="New workflow"
        submitLabel="Create and use"
        hint="Adds a workflow to your library and uses it for this project. Other projects and templates can start from it too."
        stepsHint="Steps are picked from the step library. Reorder, add or remove them, and set who picks each one up by default."
        initialName=""
        initialSteps={steps}
        testIdPrefix="workflow-create"
        onSubmit={async ({ name, steps }) => {
          const wf = await createWorkflow(workflowDraftsToLibraryInput(name, steps))
          queryClient.invalidateQueries({ queryKey: queryKeys.workflows.all })
          queryClient.invalidateQueries({ queryKey: queryKeys.stepDefinitions.all })
          toast.success(`Workflow “${wf.name}” created`)
          createDialog.settle(wf.id)
        }}
      />
    </div>
  )
}
