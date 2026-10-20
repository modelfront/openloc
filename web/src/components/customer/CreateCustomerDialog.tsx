// Inline-create dialog for a new customer.
//
// Mirrors CreateTMDialog exactly — same portal-bubble guard
// (no nested <form>, Enter key handled at the wrapper). Used by
// CustomerSelect's "+ Create new..." action so a PM
// can create a customer without leaving the project create / detail page.

import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQueryClient } from '@tanstack/react-query'
import { createCustomer, type Customer } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'

const customerSchema = z.object({
  name: z.string().min(1, 'Customer name is required').max(200, 'Name too long (max 200)'),
})
type CustomerFormValues = z.infer<typeof customerSchema>

interface Props {
  open: boolean
  onClose: () => void
  /** Called with the new customer id after a successful create. */
  onCreated: (id: number) => void
}

export function CreateCustomerDialog({ open, onClose, onCreated }: Props) {
  const queryClient = useQueryClient()
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<CustomerFormValues>({
    resolver: zodResolver(customerSchema),
    defaultValues: { name: '' },
  })

  // `onSuccess` inserts the new customer into the cache so the dropdown shows
  // it before the refetch resolves. On a 409 the error toast reads "Failed to
  // create customer: a customer with this name already exists".
  const createMut = useMutationWithToast({
    mutationFn: (name: string) => createCustomer(name),
    successMessage: 'Customer created',
    errorMessage: 'Failed to create customer',
    invalidateKeys: [queryKeys.customers.all],
    onSuccess: (customer) => {
      // Optimistic insert keeps the server's order (`ORDER BY lower(name)` in
      // ListCustomers) so the list does not flash. The next refetch replaces
      // it with the server's list.
      queryClient.setQueryData<Customer[]>(queryKeys.customers.all, (old) =>
        old ? [...old, customer].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())) : [customer],
      )
      reset()
      onCreated(customer.id)
    },
  })

  const handleClose = () => {
    reset()
    onClose()
  }

  // See CreateTMDialog. No nested <form>; submit via button onClick
  // and Enter key handled at the wrapper with stopPropagation so the click /
  // keypress can't bubble through React's component tree to ProjectCreate's
  // outer <form onSubmit>.
  const submit = handleSubmit((data) => createMut.mutate(data.name.trim()))

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) handleClose() }} keepOpen={createMut.isPending}>
      <DialogContent className="sm:max-w-md" data-testid="create-customer-dialog">
        <DialogHeader>
          <DialogTitle>Create customer</DialogTitle>
        </DialogHeader>
        <div
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              e.stopPropagation()
              submit()
            }
          }}
        >
          <FormField label="Name" error={errors.name?.message} required>
            <Input
              {...register('name')}
              placeholder="Customer name (e.g. Acme Corp)"
              autoFocus
            />
          </FormField>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={handleClose} disabled={createMut.isPending}>
              Cancel
            </Button>
            <Button
              type="button"
              onClick={() => submit()}
              disabled={createMut.isPending}
            >
              {createMut.isPending ? 'Creating...' : 'Create'}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
