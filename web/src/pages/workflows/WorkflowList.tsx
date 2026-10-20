// /workflows — the workflow library: named, ordered lists
// of library steps that projects and templates copy from. Manager surface.
import { useMemo } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listWorkflows, createWorkflow, deleteWorkflow, listStepDefinitions } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { defaultStepsFrom, workflowDraftsToLibraryInput } from '../../lib/workflow-steps'
import { useCRUDList } from '../../hooks/use-crud-list'
import ConfirmDialog from '../../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { hoverActionClass } from '@/components/list/hover-action'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Pencil } from 'lucide-react'
import type { WorkflowStep } from '../../components/WorkflowStepsEditor'
import { WorkflowStepChips } from '../../components/workflow/WorkflowStepChips'
import { WorkflowEditDialog } from '../../components/workflow/WorkflowEditDialog'
import { WorkflowFormDialog } from '../../components/workflow/WorkflowFormDialog'
import { SubNav } from '@/components/layout/SubNav'

export default function WorkflowList() {
  // The edit dialog's target lives in the URL (?edit=<id>) so it is
  // deep-linkable and survives a reload.
  const [searchParams, setSearchParams] = useSearchParams()
  const editParam = Number(searchParams.get('edit'))
  const editingId = Number.isInteger(editParam) && editParam > 0 ? editParam : null
  const openEdit = (id: number | null) => {
    const next = new URLSearchParams(searchParams)
    if (id == null) next.delete('edit')
    else next.set('edit', String(id))
    setSearchParams(next, { replace: true })
  }
  // The create dialog opens on the tenant's default pair (Translation →
  // Review), else on its first library step — a workflow needs at least one.
  const { data: definitions } = useQuery({ queryKey: queryKeys.stepDefinitions.all, queryFn: listStepDefinitions })
  const seedSteps = useMemo<WorkflowStep[]>(() => {
    const pair = defaultStepsFrom(definitions)
    if (pair.length > 0 || !definitions?.length) return pair
    const [first] = definitions
    return [{ step_definition_id: first.id, name: first.name, step_type: first.step_type as WorkflowStep['step_type'], ai_router_id: first.ai_router_id, deadline: null }]
  }, [definitions])

  const {
    items: workflows, isLoading, loadFailed, error, refetch,
    showForm: creating, setShowForm: setCreating, createMut,
    deleteTarget, setDeleteTarget, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.workflows.all,
    queryFn: listWorkflows,
    createFn: ({ name, steps }: { name: string; steps: WorkflowStep[] }) =>
      createWorkflow(workflowDraftsToLibraryInput(name, steps)),
    deleteFn: deleteWorkflow,
    entityName: 'Workflow',
    // The dialog renders the rejection and stays open.
    createErrorToast: false,
  })

  const libraryEmpty = definitions !== undefined && definitions.length === 0
  // The create payload is built from `definitions`, so creating before that
  // query resolves would post a stepless workflow and take a 400 — a workflow
  // needs at least one step. Gate on loaded-and-non-empty, not just non-empty.
  const canCreate = definitions !== undefined && definitions.length > 0

  return (
    <TooltipProvider delay={200}>
    <PageContainer>
      <PageHeader title="Workflows" />
      <SubNav
        className="mb-4"
        items={[
          { to: '/workflows', label: 'Templates', end: true },
          { to: '/workflows/steps', label: 'Steps' },
        ]}
      />

      {libraryEmpty && (
        <p className="mb-4 text-sm text-amber-700 dark:text-amber-400">
          Workflows are built from library steps. <Link to="/workflows/steps" className="underline">Create a step</Link> first.
        </p>
      )}

      <div className="mb-4">
        <Button size="sm" onClick={() => setCreating(true)} disabled={!canCreate} data-testid="new-workflow" data-guide="workflows.new">New template</Button>
      </div>

      {loadFailed && <LoadFailed what="workflows" error={error} onRetry={refetch} />}

      {isLoading ? (
        <LoadingPlaceholder />
      ) : (
        <div className="space-y-2" data-guide="workflows.list">
          {workflows?.map(w => (
            <div
              key={w.id}
              className="p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex items-center justify-between group"
              data-testid={`workflow-${w.id}`}
            >
              {/* flex column, not a plain block: the name only truncates while
                  it is a flex item that may shrink. */}
              <div className="flex min-w-0 flex-col">
                <EditableTitle name={w.name} canEdit={false} className="font-medium text-gray-900 dark:text-gray-100" />
                <WorkflowStepChips steps={w.steps} className="mt-2" />
              </div>
              <div className="flex items-center gap-1">
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button variant="ghost" size="icon"
                        aria-label="Edit"
                        onClick={() => openEdit(w.id)}
                        className={hoverActionClass('muted')}
                        data-testid={`edit-workflow-${w.id}`}
                      />
                    }
                  >
                    <Pencil />
                  </TooltipTrigger>
                  <TooltipContent>Edit workflow</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <DeleteButton
                        aria-label="Delete"
                        onClick={() => setDeleteTarget({ id: w.id, name: w.name })}
                        data-testid={`delete-workflow-${w.id}`}
                      />
                    }
                  />
                  <TooltipContent>Delete workflow</TooltipContent>
                </Tooltip>
              </div>
            </div>
          ))}
          {workflows?.length === 0 && !libraryEmpty && (
            <EmptyState message="No workflows yet. Create one to reuse an ordered list of steps across projects." />
          )}
        </div>
      )}

      {/* The same name + steps dialog the New Project wizard opens
, so a workflow is built the same way everywhere. */}
      <WorkflowFormDialog
        open={creating}
        onClose={() => setCreating(false)}
        title="New workflow"
        submitLabel="Create"
        stepsHint="Steps are picked from the step library. Projects and templates that use this workflow get a copy of them."
        initialName=""
        initialSteps={seedSteps}
        testIdPrefix="workflow-create"
        onSubmit={input => createMut.mutateAsync(input)}
      />
      <WorkflowEditDialog workflowId={editingId} onClose={() => openEdit(null)} />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete workflow "${deleteTarget?.name}"?`}
        description="Projects and templates based on it keep their copied steps."
        onConfirm={confirmDelete}
      />
    </PageContainer>
    </TooltipProvider>
  )
}
