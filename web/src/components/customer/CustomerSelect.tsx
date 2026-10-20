// CustomerSelect — pick (or inline-create) a customer for a project.
//
// A `Combobox` over the tenant's live customers (deleted ones are filtered
// server-side), with the customer-specific pieces around it:
//
// - Customer is OPTIONAL on a project, so a Clear button sits next to the field
//   (hidden while nothing is selected) — overloading the placeholder for "no
//   customer" would be confusing.
// - Inline create is admin-only (POST /api/customers requires owner/admin).
//   `canCreate` adds the pinned "Create new customer..." action; everyone else
//   can still pick an existing customer.
// - A stale customer_id (deleted while a project still references it) would
//   otherwise fall back to the placeholder and misrepresent the project. The
//   field shows a "(deleted)" badge instead so the admin re-picks or clears.
//   ProjectDetail's read-only path renders the equivalent for non-admins.

import { useDialogPromise } from '@/hooks/useDialogPromise'
import { useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { listCustomers } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { cn } from '@/lib/utils'
import { Combobox, createAction } from '../ui/combobox'
import { CreateCustomerDialog } from './CreateCustomerDialog'
import { Button } from '../ui/button'
import { Chip } from '@/components/ui/chip'

interface Props {
  value: number | null
  onChange: (id: number | null) => void
  /** Show the "Create new customer..." action. Only true for admin/owner. */
  canCreate?: boolean
  disabled?: boolean
  className?: string
  triggerClassName?: string
}

export function CustomerSelect({
  value,
  onChange,
  canCreate = false,
  disabled = false,
  className = '',
  triggerClassName = '',
}: Props) {
  const { data: customers } = useQuery({
    queryKey: queryKeys.customers.all,
    queryFn: listCustomers,
  })

  // Opens the create dialog and settles with the new id (null when closed).
  const createDialog = useDialogPromise<number>()

  const options = (customers ?? []).map(c => ({ value: String(c.id), label: c.name }))

  // Stale-FK detection: project references a customer that's no longer in the
  // live list. Only meaningful once `customers` has loaded — before that, every
  // value looks "missing".
  const isStale = value != null && customers !== undefined && !options.some(o => o.value === String(value))

  return (
    <div className={`flex items-center gap-1 ${className}`}>
      <Combobox
        // Grow to fill the flex row (the trailing Clear button keeps its content
        // width); min-w-0 lets the label truncate instead of overflowing.
        className={cn('min-w-0 flex-1', triggerClassName)}
        options={options}
        value={value == null ? '' : String(value)}
        onChange={(id) => onChange(Number(id))}
        actions={canCreate ? [createAction('Create new customer...', createDialog.request, onChange)] : undefined}
        placeholder="No customer"
        disabled={disabled}
      />
      {isStale && (
        <Chip
          color="red"
          data-testid="customer-select-stale-indicator"
          title="Customer was deleted. Pick another or clear."
        >
          Deleted
        </Chip>
      )}
      {value != null && !disabled && (
        <Button
          variant="ghost"
          size="icon-xs"
          type="button"
          onClick={() => onChange(null)}
          color="muted"
          hoverColor="red"
          aria-label="Clear customer"
          data-testid="customer-select-clear"
        >
          <X className="w-3.5 h-3.5" />
        </Button>
      )}

      {canCreate && (
        <CreateCustomerDialog
          open={createDialog.open}
          // Closed without creating: settles null so nothing gets selected.
          onClose={() => createDialog.settle(null)}
          onCreated={createDialog.settle}
        />
      )}
    </div>
  )
}
