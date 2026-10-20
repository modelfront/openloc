// /workflows/steps — the step-definition library. The only
// place a workflow step is created; workflows, templates and projects pick
// from here. Manager surface.
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  listStepDefinitions, createStepDefinition, updateStepDefinition, deleteStepDefinition,
  listAIRouters,
  type StepDefinitionRow, type StepDefinitionInput,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useCRUDList } from '../../hooks/use-crud-list'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { hoverActionClass } from '@/components/list/hover-action'
import { chipTextClass } from '@/components/ui/chip'
import { ROUTER_ENGINE_PRESETS } from '@/components/ui/chip-presets'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Pencil } from 'lucide-react'
import { STEP_TYPE_CHIPS, HumanStepChip, type StepTypePresetKey } from '@/components/ui/chip-presets'
import { runsAIStep } from '@/lib/workflow-steps'
import { StepDefinitionDialog } from '../../components/workflow/StepDefinitionDialog'
import { SubNav } from '@/components/layout/SubNav'
import { useLocation, useNavigate } from 'react-router-dom'

export default function StepLibrary() {
  // /workflows/steps/new opens the create dialog straight away, so a workflow's
  // step picker can link to making one rather than to a list with a button.
  const navigate = useNavigate()
  const creating = useLocation().pathname.endsWith('/new')
  const [dialog, setDialog] = useState<{ open: boolean; initial?: StepDefinitionRow }>({ open: creating })
  // Closing has to put the URL back, or the route still says /new and the
  // page's own New button would be a no-op.
  const closeDialog = () => {
    setDialog({ open: false })
    if (creating) navigate('/workflows/steps', { replace: true })
  }

  // Resolved client-side from the router list, the same lookup
  // `WorkflowStepChips` does — the step rows carry the router's NAME but not
  // its health, and react-query shares this fetch with every other surface
  // that already asks for routers.
  const { data: routers } = useQuery({ queryKey: queryKeys.aiRouters.all, queryFn: listAIRouters })
  const routerUnusable = (id: number | null | undefined) =>
    id == null ? '' : routers?.find(r => r.id === id)?.unusable ?? ''

  // Delete errors surface as "Delete failed: <409 body>" — the body names the
  // workflows still composing this step.
  const {
    items: definitions, isLoading, loadFailed, error, refetch, createMut,
    deleteTarget, setDeleteTarget, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.stepDefinitions.all,
    queryFn: listStepDefinitions,
    createFn: createStepDefinition,
    deleteFn: deleteStepDefinition,
    entityName: 'Step',
    createErrorToast: false,
  })

  const updateMut = useMutationWithToast({
    mutationFn: ({ id, input }: { id: number; input: StepDefinitionInput }) => updateStepDefinition(id, input),
    successMessage: 'Step updated',
    errorToast: false,
    invalidateKeys: [queryKeys.stepDefinitions.all],
  })

  // mutateAsync so a rejection reaches the dialog, which renders it inline
  // (the error toast is off for both so it is not shown twice).
  const submit = async (input: StepDefinitionInput) => {
    if (dialog.initial) await updateMut.mutateAsync({ id: dialog.initial.id, input })
    else await createMut.mutateAsync(input)
  }

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

      <div className="mb-4">
        <Button size="sm" onClick={() => setDialog({ open: true })} data-testid="new-step-definition" data-guide="workflow_steps.new">New step</Button>
      </div>

      {loadFailed && <LoadFailed what="steps" error={error} onRetry={refetch} />}

      {isLoading ? (
        <LoadingPlaceholder />
      ) : (
        <div className="space-y-2" data-guide="workflow_steps.list">
          {definitions?.map(d => {
            // `step_type` is a plain string on StepDefinition (tygo mirrors the
            // Go column), so an unrecognised value reads as a human step.
            const StepTypeChip = STEP_TYPE_CHIPS[d.step_type as StepTypePresetKey] ?? HumanStepChip
            return (
            <div
              key={d.id}
              className="p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex items-center justify-between group"
              data-testid={`step-definition-${d.id}`}
            >
              <div className="min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <EditableTitle name={d.name} canEdit={false} className="font-medium text-gray-900 dark:text-gray-100" />
                  <StepTypeChip />
                </div>
                <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">
                  {d.ai_router_name ? `${d.ai_router_name} · ` : runsAIStep(d.step_type) ? 'AI router deleted · ' : ''}
                  {/* The router's own warning, so a step that cannot run says so
                      in the library too — not only once it is inside a
                      workflow. */}
                  {routerUnusable(d.ai_router_id) === 'disabled' && (
                    <span className={chipTextClass('yellow')} title={ROUTER_ENGINE_PRESETS.disabled.title}>engine off · </span>
                  )}
                  {routerUnusable(d.ai_router_id) === 'no_key' && (
                    <span className={chipTextClass('red')} title={ROUTER_ENGINE_PRESETS.no_key.title}>engine has no key · </span>
                  )}
                  {d.workflow_count === 0 ? 'Not in any workflow' : `In ${d.workflow_count} workflow${d.workflow_count === 1 ? '' : 's'}`}
                </p>
              </div>
              <div className="flex items-center gap-1">
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button variant="ghost" size="icon"
                        aria-label="Edit"
                        onClick={() => setDialog({ open: true, initial: d })}
                        className={hoverActionClass('muted')}
                        data-testid={`edit-step-definition-${d.id}`}
                      />
                    }
                  >
                    <Pencil />
                  </TooltipTrigger>
                  <TooltipContent>Edit step</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <DeleteButton
                        aria-label="Delete"
                        onClick={() => setDeleteTarget(d)}
                        data-testid={`delete-step-definition-${d.id}`}
                      />
                    }
                  />
                  <TooltipContent>Delete step</TooltipContent>
                </Tooltip>
              </div>
            </div>
            )
          })}
          {definitions?.length === 0 && (
            <EmptyState message="No steps yet. Create one — workflows are built from the steps defined here." />
          )}
        </div>
      )}

      <StepDefinitionDialog
        open={dialog.open}
        initial={dialog.initial}
        onClose={closeDialog}
        onSubmit={submit}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete step "${deleteTarget?.name}"?`}
        description="Projects and templates already using this step keep their copy. Workflows in the library must drop it first."
        onConfirm={confirmDelete}
      />
    </PageContainer>
    </TooltipProvider>
  )
}
