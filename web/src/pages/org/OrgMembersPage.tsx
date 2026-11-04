import { errorText } from '@/lib/error-text'
import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Mail, Send, Languages, Clock } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { hoverActionClass } from '@/components/list/hover-action'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { isVendorOrg, inviteRoles } from '../../lib/roles'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { queryKeys } from '../../lib/query-keys'
import { dayMonth } from '../../lib/short-date'
import { toast } from 'sonner'
import { OrgPageFrame } from './OrgPageFrame'
import {
  getMe,
  listInvites,
  deleteInvite,
  resendInvite,
  type Invite,
} from '../../api'
import { InviteDialog } from './InviteDialog'
import { VendorOrgsCard } from './VendorOrgsCard'
import { MembersCard } from './MembersCard'
import { InviteRole } from './InviteRole'
import { AcceptedInvitesCard } from './AcceptedInvitesCard'


/**
 * The Organization page's Members tab: members, their roles, and invites.
 * Owner/admin. A customer org also invites vendors here: each vendor invite
 * creates a vendor org. A vendor org invites only its own people.
 */
export default function OrgMembersPage() {
  const { data: me, isPending: mePending } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const vendor = isVendorOrg(me)
  const [pendingInvites, setPendingInvites] = useState<Invite[]>([])
  const [loading, setLoading] = useState(true)

  const load = async () => {
    try {
      setPendingInvites(await listInvites({ status: 'pending' }))
    } catch (err) {
      toast.error(errorText(err, 'Failed to load invites'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const handleDelete = async (id: number) => {
    try {
      await deleteInvite(id)
      toast.success('Invite revoked')
      load()
    } catch (err) {
      toast.error(errorText(err, 'Failed to revoke invite'))
    }
  }

  // One resend at a time: every Resend button waits while one is sending.
  const resendMut = useMutationWithToast({
    mutationFn: (inv: Invite) => resendInvite(inv.id),
    successMessage: (inv: Invite) => `Invite sent again to ${inv.email}`,
    errorMessage: 'Failed to resend invite',
    invalidateKeys: [queryKeys.invites.all],
    onSuccess: () => { load() },
  })

  // The invite forms and the role picker depend on the org's kind.
  if (loading || mePending) {
    return <OrgPageFrame><LoadingPlaceholder /></OrgPageFrame>
  }

  const nextExpiry = pendingInvites.map(i => i.expires_at).sort()[0]

  return (
    <OrgPageFrame>

      <MembersCard
        vendor={vendor}
        actions={
          <InviteDialog
            title="Invite member"
            description="They get an email with a link to join this organization."
            icon={<Mail />}
            roles={inviteRoles(vendor)}
            placeholder="colleague@example.com"
            testIdPrefix="member-invite"
            guide="org.invite"
            onInvited={load}
          />
        }
      />

      {/* A vendor is its own org, linked to this one; the invitee becomes
          its admin. Vendor orgs do not have vendors of their own. */}
      {!vendor && (
        <VendorOrgsCard
          inviteAction={
            <InviteDialog
              title="Invite vendor"
              description="Creates an organization for the vendor. The person you invite becomes its admin and invites their own team."
              icon={<Languages />}
              roles={['admin']}
              emailLabel="Admin email"
              orgNameField={{ label: 'Vendor name', placeholder: 'Acme Translations' }}
              placeholder="admin@vendor.example.com"
              testIdPrefix="vendor-invite"
              guide="org.vendor_invite"
              onInvited={load}
            />
          }
        />
      )}

      {/* Pending invites */}
      {pendingInvites.length > 0 && (
        <SettingsCard
          title="Pending invites"
          icon={<Clock />}
          summary={`${pendingInvites.length} · next expires ${dayMonth(nextExpiry)}`}
          data-testid="pending-invites"
        >
          <div className="divide-y divide-gray-100 dark:divide-gray-800">
            {pendingInvites.map(inv => (
              <div key={inv.id} data-testid={`org-invite-row-${inv.id}`} className="flex max-md:flex-wrap items-center justify-between gap-x-4 gap-y-2 py-3">
                <div className="min-w-0">
                  <span className="text-sm font-medium text-gray-900 dark:text-gray-100 break-all">{inv.email}</span>
                  <span className="ml-2 inline-flex items-center gap-2"><InviteRole invite={inv} /></span>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-gray-400">
                    Expires {new Date(inv.expires_at).toLocaleDateString()}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => resendMut.mutate(inv)}
                    disabled={resendMut.isPending}
                    loading={resendMut.isPending && resendMut.variables?.id === inv.id}
                    className={hoverActionClass('muted')}
                    aria-label="Resend invite"
                    title="Resend invite"
                    data-testid={`org-invite-resend-${inv.id}`}
                  >
                    <Send />
                  </Button>
                  <DeleteButton
                    onClick={() => handleDelete(inv.id)}
                    aria-label="Revoke invite"
                  />
                </div>
              </div>
            ))}
          </div>
        </SettingsCard>
      )}

      <AcceptedInvitesCard />
    </OrgPageFrame>
  )
}
