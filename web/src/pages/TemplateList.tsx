import { Link, useNavigate } from 'react-router-dom'
import { listTemplates, deleteTemplate, duplicateTemplate } from '../api'
import { queryKeys } from '../lib/query-keys'
import { formatDate } from '../utils'
import { useCRUDList } from '../hooks/use-crud-list'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { hoverActionClass } from '@/components/list/hover-action'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Button } from '@/components/ui/button'
import { CreateButton } from '@/components/ui/create-button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Copy } from 'lucide-react'
import { Chip } from '@/components/ui/chip'
import { listCardClass } from '@/components/list/list-row'
import { cn } from '@/lib/utils'

export default function TemplateList() {
  const navigate = useNavigate()

  // Deep copy (scalars + all assignments) → open the copy for editing.
  const duplicateMut = useMutationWithToast({
    mutationFn: duplicateTemplate,
    successMessage: 'Template duplicated',
    errorMessage: 'Duplicate failed',
    invalidateKeys: [queryKeys.templates.all],
    onSuccess: (copy) => navigate(`/templates/${copy.id}`),
  })

  const {
    items: templates, isLoading, loadFailed, error, refetch,
    deleteTarget, setDeleteTarget, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.templates.all,
    queryFn: listTemplates,
    deleteFn: deleteTemplate,
    entityName: 'Template',
  })

  return (
    <TooltipProvider delay={200}>
    <PageContainer>
      <PageHeader
        title="Templates"
        actions={<CreateButton onClick={() => navigate('/templates/new')} data-guide="templates.new">New template</CreateButton>}
      />

      {loadFailed && <LoadFailed what="templates" error={error} onRetry={refetch} />}

      {isLoading ? (
        <LoadingPlaceholder />
      ) : (
        <div className="space-y-2" data-guide="templates.list">
          {templates?.map(t => (
            <Link key={t.id} to={`/templates/${t.id}`} data-testid={`template-${t.id}`} className={cn(listCardClass, 'flex items-center justify-between')}>
              <div className="min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <EditableTitle name={t.name} canEdit={false} className="font-medium text-gray-900 dark:text-gray-100" />
                  {t.mt_on_upload && (
                    <Chip color="blue">Pre-translate</Chip>
                  )}
                </div>
                <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">Created {formatDate(t.created_at)}</p>
              </div>
              <div className="flex items-center gap-1">
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button variant="ghost" size="icon"
                        aria-label="Duplicate"
                        onClick={e => {
                          e.preventDefault()
                          duplicateMut.mutate(t.id)
                        }}
                        disabled={duplicateMut.isPending}
                        className={hoverActionClass('muted')}
                        data-testid={`duplicate-template-${t.id}`}
                      />
                    }
                  >
                    <Copy />
                  </TooltipTrigger>
                  <TooltipContent>Duplicate template</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <DeleteButton
                        aria-label="Delete"
                        onClick={e => {
                          e.preventDefault()
                          setDeleteTarget({ id: t.id, name: t.name })
                        }}
                      />
                    }
                  />
                  <TooltipContent>Delete template</TooltipContent>
                </Tooltip>
              </div>
            </Link>
          ))}
          {templates?.length === 0 && (
            <EmptyState message="No templates yet. Create one to configure TM and MT settings." />
          )}
        </div>
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete template "${deleteTarget?.name}"?`}
        description="This will remove the template and all its TM/MT assignments."
        onConfirm={confirmDelete}
      />
    </PageContainer>
    </TooltipProvider>
  )
}
