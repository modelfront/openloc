import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { CheckCheck } from 'lucide-react'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { Input } from '@/components/ui/input'
import { PagedTable } from '@/components/list'
import { listAcceptedInvites, type Invite } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { matchCount } from '../../lib/plural'
import { dayMonth } from '../../lib/short-date'
import { useDebouncedUrlParam } from '../../hooks/useDebouncedUrlParam'
import { usePagedQuery } from '../../hooks/usePagedQuery'
import { InviteRole } from './InviteRole'

// The list's URL params, prefixed so they never meet the Members or Vendors lists'.
const SEARCH_KEY = 'aq'
const PAGE_KEY = 'apage'

const ACCEPTED_COLUMNS: ColumnDef<Invite>[] = [
  {
    id: 'email',
    header: 'Email',
    cell: ({ row: { original: inv } }) => (
      <span className="block truncate text-sm text-gray-900 dark:text-gray-100">{inv.email}</span>
    ),
  },
  {
    id: 'role',
    header: 'Role',
    size: 260,
    cell: ({ row: { original: inv } }) => <span className="inline-flex items-center gap-2"><InviteRole invite={inv} /></span>,
  },
  {
    id: 'accepted',
    header: 'Accepted',
    size: 110,
    cell: ({ row: { original: inv } }) => (
      <span className="text-xs text-gray-400">{new Date(inv.accepted_at!).toLocaleDateString()}</span>
    ),
  },
]

/**
 * The Members tab's Accepted invites: the invitations people took, newest
 * first, searched by email or vendor name, in numbered pages. Hidden while no
 * invite was accepted yet.
 */
export function AcceptedInvitesCard() {
  const [params] = useSearchParams()
  const q = params.get(SEARCH_KEY)?.trim() ?? ''
  const [search, setSearch] = useDebouncedUrlParam(SEARCH_KEY, 300, [PAGE_KEY])

  const paged = usePagedQuery({
    pageKey: PAGE_KEY,
    queryKey: page => queryKeys.invites.accepted(q, page),
    queryFn: (offset, limit) => listAcceptedInvites(q, offset, limit),
  })
  // The summary: every accepted invite, and the newest one's date.
  const latest = useQuery({
    queryKey: queryKeys.invites.accepted('', 0),
    queryFn: () => listAcceptedInvites('', 0, 1),
  })
  const allTotal = latest.data?.total
  const newest = latest.data?.data[0]?.accepted_at
  if (!allTotal) return null

  return (
    <SettingsCard
      flush
      title="Accepted invites"
      icon={<CheckCheck />}
      summary={`${allTotal}${newest ? ` · last on ${dayMonth(newest)}` : ''}${q && paged.total != null ? ` · ${matchCount(paged.total)} the search` : ''}`}
      toolbar={
        <Input
          placeholder="Search email or vendor..."
          value={search}
          onChange={e => setSearch(e.target.value)}
          className="text-sm max-w-xs"
          data-testid="accepted-search"
        />
      }
      data-testid="accepted-invites"
    >
      <PagedTable
        paged={paged}
        what="accepted invites"
        emptyMessage="No accepted invites match this search."
        columns={ACCEPTED_COLUMNS}
        rowTestId={inv => `org-invite-accepted-${inv.id}`}
        testId="accepted-pages"
      />
    </SettingsCard>
  )
}
