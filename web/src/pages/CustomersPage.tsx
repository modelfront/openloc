// CustomersPage — admin-only CRUD for the customer entity.
//
// Mirrors TMList structure (useCRUDList + InlineRenameField + ConfirmDialog).
// Differences:
//   - No detail page link (customers have no per-entity workspace yet);
//     rows render as plain divs.
//   - Delete confirmation fetches project count to warn the admin
//     ("3 projects reference this customer — they'll be detached.")
//   - Soft-delete-then-recreate is supported by the backend's partial unique
//     index; no special UI handling required.

import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import {
  listCustomers,
  createCustomer,
  renameCustomer,
  deleteCustomer,
  getCustomerProjectCount,
} from '../api'
import { queryKeys } from '../lib/query-keys'
import { formatDate } from '../utils'
import { useCRUDList } from '../hooks/use-crud-list'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { DeleteButton } from '@/components/ui/delete-button'
import { Pencil } from 'lucide-react'
import { InlineRenameField } from '@/components/list/InlineRenameField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { useScrollToError } from '../hooks/useScrollToError'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'

const customerSchema = z.object({
  name: z.string().min(1, 'Customer name is required').max(200, 'Name too long (max 200)'),
})
type CustomerFormValues = z.infer<typeof customerSchema>

export default function CustomersPage() {
  const {
    register: registerCreate,
    handleSubmit: handleCreateSubmit,
    reset: resetCreate,
    formState: { errors: createErrors },
  } = useForm<CustomerFormValues>({
    resolver: zodResolver(customerSchema),
    defaultValues: { name: '' },
  })
  useScrollToError(createErrors)

  const {
    items: customers, isLoading, loadFailed, error, refetch,
    showForm, setShowForm, createMut,
    renamingId, renameValue, setRenameValue, startRename, submitRename, cancelRename,
    deleteTarget, setDeleteTarget, handleDelete, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.customers.all,
    queryFn: listCustomers,
    createFn: (name: string) => createCustomer(name),
    renameFn: ({ id, name }) => renameCustomer(id, name),
    deleteFn: deleteCustomer,
    entityName: 'customer',
    onCreateSuccess: () => resetCreate(),
  })

  // Fetch project count for the customer being considered for deletion. Done
  // lazily (only when deleteTarget is set) — avoids N+1 on the list query.
  // The `?? 0` placeholder is never fetched (enabled guard prevents it); React
  // Query just needs a stable key shape from the factory.
  const { data: projectCountData, isLoading: countLoading } = useQuery({
    queryKey: queryKeys.customer.projectCount(deleteTarget?.id ?? 0),
    queryFn: () => getCustomerProjectCount(deleteTarget!.id),
    enabled: !!deleteTarget,
  })

  if (isLoading) return <LoadingPlaceholder />

  const projectCount = projectCountData?.project_count
  const deleteDescription = countLoading || projectCount == null
    ? 'Loading project references...'
    : projectCount === 0
      ? 'No projects reference this customer.'
      : `${projectCount} project${projectCount === 1 ? '' : 's'} reference this customer. They will be detached (customer field set to none) but not deleted.`

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="Customers"
          actions={
            <Button onClick={() => setShowForm(!showForm)}>
              New customer
            </Button>
          }
        />

        {showForm && (
          <form onSubmit={handleCreateSubmit(data => createMut.mutate(data.name.trim()))} className="mb-6 p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex max-md:flex-wrap gap-3 items-end">
            <FormField label="Name" error={createErrors.name?.message} required className="flex-1">
              <Input
                {...registerCreate('name')}
                placeholder="Customer name (e.g. Acme Corp)"
                autoFocus
              />
            </FormField>
            <Button type="submit" size="sm" disabled={createMut.isPending}>
              {createMut.isPending ? 'Creating...' : 'Create'}
            </Button>
          </form>
        )}

        {loadFailed && <LoadFailed what="customers" error={error} onRetry={refetch} />}

        <div className="space-y-2">
          {customers?.map(c => (
            <div
              key={c.id}
              data-testid={`customer-row-${c.id}`}
              className="block p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 group"
            >
              <div className={listCardHeaderClass}>
                <div className={`${listCardTitleClass} gap-2`}>
                  {renamingId === c.id ? (
                    <InlineRenameField
                      value={renameValue}
                      onChange={setRenameValue}
                      onSubmit={() => submitRename(c.id)}
                      onCancel={cancelRename}
                    />
                  ) : (
                    <>
                      <div className="font-medium text-gray-900 dark:text-gray-100 truncate">{c.name}</div>
                      {/* Rename pencil next to the name (customers have no detail
                          page, so it lives here rather than in a header). */}
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        type="button"
                        onClick={e => startRename(e, c.id, c.name)}
                        data-testid={`rename-customer-${c.id}`}
                        title="Rename"
                        className="shrink-0 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
                      >
                        <Pencil className="w-4 h-4" />
                      </Button>
                    </>
                  )}
                </div>
                <div className="flex items-center gap-2 ml-3 shrink-0">
                  <span className="text-xs text-gray-400 dark:text-gray-500">
                    Created {formatDate(c.created_at)}
                  </span>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <DeleteButton
                          aria-label="Delete customer"
                          onClick={e => handleDelete(e, c.id, c.name)}
                        />
                      }
                    />
                    <TooltipContent>Delete customer (soft-delete)</TooltipContent>
                  </Tooltip>
                </div>
              </div>
            </div>
          ))}
          {customers?.length === 0 && (
            <EmptyState message="No customers yet. Create one to organize projects by client." />
          )}
        </div>

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title={`Delete customer "${deleteTarget?.name}"?`}
          description={deleteDescription}
          onConfirm={confirmDelete}
        />
      </PageContainer>
    </TooltipProvider>
  )
}
