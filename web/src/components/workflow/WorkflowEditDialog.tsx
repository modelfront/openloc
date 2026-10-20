// Edit a library workflow in place on the Workflows list:
// rename + reorder / add / remove library steps + default assignees. Opened by
// the row's Edit button and right after "New workflow"; deep-linkable via
// /workflows?edit=<id>.
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getWorkflow, updateWorkflow } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { workflowDefinitionStepsToDrafts, workflowDraftsToLibraryInput } from '../../lib/workflow-steps'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { WorkflowFormDialog } from './WorkflowFormDialog'

interface Props {
  /** Workflow to edit; null keeps the dialog closed. */
  workflowId: number | null
  onClose: () => void
}

export function WorkflowEditDialog({ workflowId, onClose }: Props) {
  const open = workflowId != null
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.workflows.detail(workflowId ?? 0),
    queryFn: () => getWorkflow(workflowId as number),
    enabled: open,
  })

  // Stable per loaded workflow: the dialog reseeds its draft when this changes.
  const initialSteps = useMemo(() => (data ? workflowDefinitionStepsToDrafts(data.steps) : []), [data])

  const saveMut = useMutationWithToast({
    mutationFn: (input: { name: string; steps: typeof initialSteps }) =>
      updateWorkflow(workflowId as number, workflowDraftsToLibraryInput(input.name, input.steps)),
    successMessage: 'Workflow saved',
    // The dialog renders the rejection and stays open.
    errorToast: false,
    // `workflows.all` prefixes the detail key; the Steps page shows "in N workflows".
    invalidateKeys: [queryKeys.workflows.all, queryKeys.stepDefinitions.all],
  })

  return (
    <WorkflowFormDialog
      open={open}
      onClose={onClose}
      title="Edit workflow"
      submitLabel="Save"
      stepsHint="Projects and templates that use this workflow get a copy of these steps. Editing here does not change them."
      loading={isLoading || !data}
      initialName={data?.name ?? ''}
      initialSteps={initialSteps}
      onSubmit={input => saveMut.mutateAsync(input)}
      testIdPrefix="workflow-edit"
    />
  )
}
