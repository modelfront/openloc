// Workflow tab shared by ProjectDetail and TemplateDetail: one card holding
// the library picker and, under it, the steps.
//
// A project's steps are shown with their deadlines and change through Edit
// steps (ProjectWorkflowSteps): the steps its jobs have reached are locked,
// the rest can change, and each step's deadline sits at the right of its row. A template's steps come from the library and
// change only by picking another workflow; a template also gets the mode
// toggle. A created Raw MT project keeps the built-in step, read-only — the
// server refuses the switch after setup.
import { useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { listWorkflows, type WorkflowPreview } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { workflowMode } from '../../lib/workflow-steps'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { ProjectWorkflowSteps } from './ProjectWorkflowSteps'
import { WorkflowPicker } from './WorkflowPicker'
import { WorkflowStepChips } from './WorkflowStepChips'
import { PretranslateWorkflowIcon, WorkflowModeToggle } from './WorkflowModeToggle'
import { MTRouterLink, RawMTPane } from './RawMTPane'
import { errorText } from '@/lib/error-text'

interface Props {
  steps: WorkflowStep[]
  workflowId: number | null | undefined
  workflowName: string | null | undefined
  /** The steps were changed after the workflow was applied. */
  workflowEdited?: boolean
  /** Project context: the steps are edited in place. */
  sourceLang?: string
  /** A project's target languages — the assignee filter and the per-language deadline rows. */
  targetLangs?: string[]
  /** The MT router and whether MT runs on upload, for the Pre-translation row. */
  mtRouterName?: string | null
  mtOnUpload?: boolean
  /** Takes the user to the MT router picker (it is on the Settings tab). */
  onPickRouter?: () => void
  onApplyWorkflow: (workflowId: number) => Promise<void>
  /** A project's step writes: the save, its preview and a re-read after a
   *  refused one. Absent for a template. */
  onSaveSteps?: (steps: WorkflowStep[]) => Promise<void>
  onPreviewSteps?: (steps: WorkflowStep[]) => Promise<WorkflowPreview>
  onRefresh?: () => void
  /** Extra sentence in the "use this workflow" confirmation — projects mention in-flight jobs. */
  applyWarning?: string
}

export function WorkflowTab({
  steps, workflowId, workflowName, workflowEdited, sourceLang, targetLangs, onApplyWorkflow, onSaveSteps, onPreviewSteps, onRefresh,
  applyWarning, mtRouterName, mtOnUpload, onPickRouter,
}: Props) {
  const { data: workflows } = useQuery({ queryKey: queryKeys.workflows.all, queryFn: listWorkflows })
  const isProject = !!sourceLang
  const [stepsDraftOpen, setStepsDraftOpen] = useState(false)

  const applyWorkflow = async (id: number) => {
    try {
      await onApplyWorkflow(id)
      toast.success('Workflow applied')
    } catch (err) {
      toast.error(errorText(err, 'Workflow not applied'))
    }
  }
  const rawMT = workflowMode(steps) === 'raw_mt'
  const library = workflows ?? []
  const previousWorkflowRef = useRef<number | null>(null)
  const heading = !isProject && (
    <WorkflowModeToggle
      steps={steps}
      workflowId={workflowId ?? null}
      workflows={library}
      previousWorkflowRef={previousWorkflowRef}
      confirmApply
      applyWarning={applyWarning}
      onApply={applyWorkflow}
    />
  )
  // Without a router nothing runs on upload, so there is no row to show. A
  // created Raw MT project lists the built-in step itself, so no row either.
  const pretranslation = mtOnUpload && mtRouterName && !rawMT
    ? <MTRouterLink mtRouterName={mtRouterName} onPickRouter={onPickRouter} />
    : undefined
  const editable = isProject && !rawMT && onSaveSteps && onPreviewSteps
  const stepRows = editable ? (
    <ProjectWorkflowSteps
      steps={steps}
      sourceLang={sourceLang}
      targetLangs={targetLangs ?? []}
      pretranslation={pretranslation}
      onSave={onSaveSteps}
      onPreview={onPreviewSteps}
      onRefresh={() => onRefresh?.()}
      onDraftOpen={setStepsDraftOpen}
    />
  ) : (
    <WorkflowStepChips steps={steps} pretranslation={pretranslation} />
  )

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-guide="project_workflow.picker">
      {/* Unlike the wizard, these steps may already be in flight, so a swap is
          confirmed first. */}
      {!isProject && rawMT ? (
        <RawMTPane mtRouterName={mtRouterName} onPickRouter={onPickRouter} heading={heading} />
      ) : (
        <WorkflowPicker
          workflows={library}
          value={workflowId ?? null}
          valueName={workflowName}
          edited={workflowEdited}
          disabled={stepsDraftOpen}
          disabledReason={stepsDraftOpen ? 'Save or discard your changes to the steps first.' : undefined}
          valueIcon={rawMT ? <PretranslateWorkflowIcon /> : undefined}
          locked={rawMT}
          heading={heading}
          entity={isProject ? 'project' : 'template'}
          confirmApply
          applyWarning={applyWarning}
          onApply={applyWorkflow}
        >
          {stepRows}
        </WorkflowPicker>
      )}
    </div>
  )
}
