// The Vendors table's columns. Module scope, so they keep their identity across
// renders — the invite actions and their busy state arrive through the table's
// `meta` (VendorsTableMeta) instead.

import type { Table } from '@tanstack/react-table'
import { Send } from 'lucide-react'

import type { Invite, Member } from '@/api'
import { formatDate } from '@/utils'
import { EditableTitle } from '@/components/list/EditableTitle'
import { hoverActionClass } from '@/components/list/hover-action'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { DeleteButton } from '@/components/ui/delete-button'
import type { ColumnDef } from '@/components/ui/data-table'

/** One row: an invite that was not accepted yet, or a vendor account. */
export type VendorRow =
  | { kind: 'invite'; id: number; email: string; since: string }
  | { kind: 'vendor'; id: number; name: string; email: string; since: string }

export const inviteRow = (i: Invite): VendorRow => ({ kind: 'invite', id: i.id, email: i.email, since: i.created_at })
export const vendorRow = (m: Member): VendorRow => ({ kind: 'vendor', id: m.id, name: m.name, email: m.email, since: m.created_at })

export const vendorRowTestId = (r: VendorRow) =>
  r.kind === 'invite' ? `vendor-invite-row-${r.id}` : `vendor-row-${r.id}`

export interface VendorsTableMeta {
  onResend: (inviteId: number) => void
  onRevoke: (inviteId: number) => void
  resending: boolean
  revoking: boolean
}

const metaOf = (table: Table<VendorRow>) => table.options.meta as unknown as VendorsTableMeta

export const VENDOR_COLUMNS: ColumnDef<VendorRow>[] = [
  {
    id: 'vendor',
    header: 'Vendor',
    cell: ({ row: { original: r } }) =>
      r.kind === 'invite' ? (
        <EditableTitle name={r.email} canEdit={false} className="flex font-medium text-gray-900 dark:text-gray-100" />
      ) : (
        <div className="flex min-w-0 flex-col">
          <EditableTitle name={r.name} canEdit={false} className="flex font-medium text-gray-900 dark:text-gray-100" />
          <EditableTitle name={r.email} canEdit={false} className="flex text-xs text-gray-500 dark:text-gray-400" />
        </div>
      ),
  },
  {
    id: 'status',
    header: 'Status',
    size: 130,
    cell: ({ row: { original: r } }) =>
      r.kind === 'invite'
        ? <Chip color="yellow" data-testid={`vendor-status-pending-${r.id}`}>Not joined yet</Chip>
        : <Chip color="green">Active</Chip>,
  },
  {
    id: 'since',
    header: 'Since',
    size: 170,
    cell: ({ row: { original: r } }) => (
      <span className="text-gray-500 dark:text-gray-400">
        {r.kind === 'invite' ? 'Invited' : 'Joined'} {formatDate(r.since)}
      </span>
    ),
  },
  {
    id: 'actions',
    header: '',
    size: 96,
    cell: ({ row: { original: r }, table }) => {
      if (r.kind !== 'invite') return null
      const { onResend, onRevoke, resending, revoking } = metaOf(table)
      return (
        <div className="flex justify-end gap-1">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => onResend(r.id)}
            disabled={resending}
            className={hoverActionClass('muted')}
            aria-label="Resend invite"
            title="Resend invite"
            data-testid={`vendor-invite-resend-${r.id}`}
          >
            <Send />
          </Button>
          <DeleteButton
            onClick={() => onRevoke(r.id)}
            disabled={revoking}
            aria-label="Revoke invite"
            title="Revoke invite"
            data-testid={`vendor-invite-revoke-${r.id}`}
          />
        </div>
      )
    },
  },
]
