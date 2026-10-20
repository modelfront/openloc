import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { applyProjectWorkflow, listMTRouters, setProjectWorkflowSteps, type ProjectSettings } from '../../api'
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
  if (!settings) {
    if (error) return <LoadFailed what="workflow" error={error} onRetry={onRefresh} />
    return <LoadingPlaceholder message="Loading workflow..." delayMs={0} />
  }
  const project = settings.project

  // A step edit can reassign in-flight jobs server-side, so the job list, the
  // per-job step panels and the acting user's Tasks page all go stale.
  const afterStepsChanged = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(projectId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.job.allSteps })
    onRefresh()
  }

  return (
    <WorkflowTab
      steps={workflowStepsFromApi(settings.workflow_steps)}
      workflowId={project.workflow_id}
      workflowName={project.workflow_name}
      sourceLang={project.source_language}
      targetLangs={project.target_languages}
      mtOnUpload={project.mt_on_upload}
      mtRouterName={mtRouters?.find(r => r.id === project.mt_router_id)?.name}
      onPickRouter={() => { navigate(`/projects/${projectId}/settings`); focusMTRouter() }}
      onSaveSteps={async (steps) => {
        await setProjectWorkflowSteps(projectId, workflowStepsToPayload(steps))
        afterStepsChanged()
      }}
      applyWarning="Steps that running jobs have already reached keep their place; only steps nobody has got to yet can change, and new steps are added at the end."
      onApplyWorkflow={async (workflowId) => {
        await applyProjectWorkflow(projectId, workflowId)
        afterStepsChanged()
      }}
    />
  )
}
