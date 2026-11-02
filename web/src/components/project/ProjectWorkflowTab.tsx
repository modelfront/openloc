import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { applyProjectWorkflow, listMTRouters, previewProjectWorkflowSteps, setProjectWorkflowSteps, type ProjectSettings } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { workflowStepsFromApi, workflowStepsToPayload } from '../../lib/workflow-steps'
import { WorkflowTab } from '../workflow/WorkflowTab'
import { focusMTRouter } from '../workflow/RawMTPane'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'

/** The project's Workflow tab. */
export function ProjectWorkflowTab({ projectId, settings, error, onRefresh }: {
  projectId: number
  settings: ProjectSettings | null
  /** The settings query's error, when it failed (a deleted project, a 500). */
  error?: unknown
  onRefresh: () => void
}) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { data: mtRouters } = useQuery({ queryKey: queryKeys.mtRouters.all, queryFn: listMTRouters })
  // Stable while the saved steps are: a new array tells the editor the steps
  // changed underneath it.
  const apiSteps = settings?.workflow_steps
  const steps = useMemo(() => workflowStepsFromApi(apiSteps ?? []), [apiSteps])
  if (!settings) {
    if (error) return <LoadFailed what="workflow" error={error} onRetry={onRefresh} />
    return <LoadingPlaceholder message="Loading workflow..." delayMs={0} />
  }
  const project = settings.project

  // A step edit can reassign in-flight jobs and start a step on finished ones
  // server-side, so the job list, the project's task matrix, the per-job step
  // panels and the acting user's Tasks page all go stale.
  const afterStepsChanged = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.tasks(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.job.allSteps })
    onRefresh()
  }

  return (
    <WorkflowTab
      steps={steps}
      workflowId={project.workflow_id}
      workflowName={project.workflow_name}
      workflowEdited={project.workflow_edited}
      sourceLang={project.source_language}
      targetLangs={project.target_languages}
      mtOnUpload={project.mt_on_upload}
      mtRouterName={mtRouters?.find(r => r.id === project.mt_router_id)?.name}
      onPickRouter={() => { navigate(`/projects/${projectId}/settings`); focusMTRouter() }}
      onSaveSteps={async (next) => {
        await setProjectWorkflowSteps(projectId, workflowStepsToPayload(next))
        afterStepsChanged()
      }}
      onPreviewSteps={next => previewProjectWorkflowSteps(projectId, workflowStepsToPayload(next))}
      onRefresh={onRefresh}
      applyWarning="Steps that jobs have already reached keep their place; only the steps after them can change."
      onApplyWorkflow={async (workflowId) => {
        await applyProjectWorkflow(projectId, workflowId)
        afterStepsChanged()
      }}
    />
  )
}
