// VendorsPage — the Organization page's Vendors tab: managing external vendors (linguists). Owners, admins and
// project managers invite a vendor by email; the invite link goes only to
// that address. The page lists both:
//   - Pending invites (vendor invited but hasn't accepted → "Not joined yet",
//     i.e. never logged in), which can be resent or revoked, and
//   - Active vendor users (accepted their invite → have an account).
// Vendors themselves never see this page (route + backend are manager-gated).

import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listMembers, listInvites, createInvite, deleteInvite, resendInvite } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { loadFailure } from '../../lib/load-failure'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { OrgPageFrame } from './OrgPageFrame'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { DataTable } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { VENDOR_COLUMNS, inviteRow, vendorRow, vendorRowTestId, type VendorsTableMeta } from '@/components/vendors/vendor-columns'

const inviteSchema = z.object({
  email: z.string().email('Enter a valid email address'),
})
type InviteFormValues = z.infer<typeof inviteSchema>

export default function VendorsPage() {
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { email: '' },
  })

  const membersQuery = useQuery({
    queryKey: queryKeys.members.all,
    queryFn: listMembers,
  })
  const invitesQuery = useQuery({
    queryKey: queryKeys.invites.all,
    queryFn: listInvites,
  })

  const inviteMut = useMutationWithToast({
    mutationFn: (email: string) => createInvite(email, 'linguist'),
    successMessage: 'Invitation email sent',
    errorMessage: 'Failed to invite vendor',
    invalidateKeys: [queryKeys.invites.all],
    onSuccess: () => reset(),
  })

  const revokeMut = useMutationWithToast({
    mutationFn: (id: number) => deleteInvite(id),
    successMessage: 'Invitation revoked',
    errorMessage: 'Failed to revoke invitation',
    invalidateKeys: [queryKeys.invites.all],
  })

  const resendMut = useMutationWithToast({
    mutationFn: (id: number) => resendInvite(id),
    successMessage: 'Invitation sent again',
    errorMessage: 'Failed to resend invitation',
    invalidateKeys: [queryKeys.invites.all],
  })

  // Pending invites first, then vendor accounts. Vendors are linguists: the
  // Vendors tab is the invite flow, and every vendor account is created with
  // the linguist role.
  const rows = useMemo(() => [
    ...(invitesQuery.data ?? []).filter(i => i.role === 'linguist' && !i.accepted_at).map(inviteRow),
    ...(membersQuery.data ?? []).filter(m => m.role === 'linguist').map(vendorRow),
  ], [invitesQuery.data, membersQuery.data])

  const tableMeta: VendorsTableMeta = {
    onResend: id => resendMut.mutate(id),
    onRevoke: id => revokeMut.mutate(id),
    resending: resendMut.isPending,
    revoking: revokeMut.isPending,
  }

  if (membersQuery.isLoading || invitesQuery.isLoading) return <OrgPageFrame><LoadingPlaceholder /></OrgPageFrame>

  const failure = loadFailure(membersQuery, invitesQuery)

  return (
    <OrgPageFrame>

      <form
        onSubmit={handleSubmit(data => inviteMut.mutate(data.email.trim()))}
        className="mb-6 p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex max-md:flex-wrap gap-3 items-end"
        data-guide="vendors.invite"
      >
        <FormField label="Invite a vendor by email" error={errors.email?.message} required className="flex-1">
          <Input
            {...register('email')}
            type="email"
            placeholder="translator@example.com"
            data-testid="vendor-invite-email"
          />
        </FormField>
        <Button type="submit" disabled={inviteMut.isPending} data-testid="vendor-invite-submit">
          {inviteMut.isPending ? 'Sending…' : 'Send invitation'}
        </Button>
      </form>

      {failure ? (
        <LoadFailed what="vendors" error={failure.error} onRetry={failure.retry} />
      ) : rows.length === 0 ? (
        <EmptyState message="No vendors yet. Invite an external translator by email to get started." />
      ) : (
        <div className="overflow-hidden rounded-lg border border-gray-200 bg-card text-sm dark:border-gray-800" data-guide="vendors.list">
          <DataTable
            columns={VENDOR_COLUMNS}
            data={rows}
            meta={tableMeta as unknown as Record<string, unknown>}
            rowTestId={vendorRowTestId}
          />
        </div>
      )}
    </OrgPageFrame>
  )
}
