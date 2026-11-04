import { Chip } from '@/components/ui/chip'
import type { Invite } from '../../api'
import { roleLabel } from '../../lib/roles'
import { ROLE_COLORS } from './MembersCard'

/**
 * An invite's role chip. A vendor-org invite makes its invitee the admin of the
 * vendor org it names, not a member here, so it says that instead.
 */
export function InviteRole({ invite }: { invite: Invite }) {
  if (invite.kind === 'vendor_org') {
    return (
      <>
        <Chip>Vendor org admin</Chip>
        <span className="text-sm text-gray-600 dark:text-gray-400" data-testid={`invite-vendor-name-${invite.id}`}>
          {invite.vendor_org_name}
        </span>
      </>
    )
  }
  return <Chip color={ROLE_COLORS[invite.role] ?? 'grey'}>{roleLabel(invite.role)}</Chip>
}
