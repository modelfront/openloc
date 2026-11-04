import { useMemo, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Users } from 'lucide-react'
import { SettingsCard } from '@/components/layout/SettingsCard'
import type { ColumnDef } from '@tanstack/react-table'
import { PagedTable } from '@/components/list'
import { Chip, type ChipColor } from '@/components/ui/chip'
import { listMembersPage, type MemberPageItem } from '../../api'
import { MemberDisabledChip } from '@/components/ui/chip-presets'
import { queryKeys } from '../../lib/query-keys'
import { count, matchCount } from '../../lib/plural'
import { memberRoles, roleLabel } from '../../lib/roles'
import { rowsFromPairs, PairRowChip } from '../../components/userpair/LanguagePairsEditor'
import MemberDialog from './MemberDialog'
import { MembersFilterBar, parseMemberFilters, MEMBER_PAGE_KEY } from './MembersFilterBar'
import { usePagedQuery } from '../../hooks/usePagedQuery'

// Owner is yellow because it's the role you should notice before changing
// anything; admin blue; the rest neutral. Labels live in lib/roles,
// so only the colour assignment is local.
export const ROLE_COLORS: Record<string, ChipColor> = {
  owner: 'yellow',
  admin: 'blue',
}

// The member table. A row opens the member dialog; the name is also a button,
// so Tab + Enter opens it (the click reaches the row).
const MEMBER_COLUMNS: ColumnDef<MemberPageItem>[] = [
  {
    id: 'member',
    header: 'Member',
    cell: ({ row: { original: m } }) => (
      // eslint-disable-next-line no-restricted-syntax -- a row target: keyboard focus for opening the row, styled as plain text
      <button type="button" aria-label={`Edit ${m.name}`} className="block min-w-0 max-w-full text-left">
        <span className="block truncate text-sm font-medium text-gray-900 dark:text-gray-100">{m.name}</span>
        <span className="block truncate text-xs text-gray-500 dark:text-gray-400">{m.email}</span>
      </button>
    ),
  },
  {
    id: 'languages',
    header: 'Languages',
    size: 220,
    cell: ({ row: { original: m } }) => m.language_pairs.length > 0 ? (
      <div className="flex flex-wrap gap-1" data-testid={`pairs-display-${m.id}`}>
        {/* One chip per row (grouped by source), matching the editor. */}
        {rowsFromPairs(m.language_pairs).map((row, i) => <PairRowChip key={i} row={row} />)}
      </div>
    ) : (
      <span className="text-xs text-gray-400" data-testid={`pairs-none-${m.id}`}>—</span>
    ),
  },
  {
    id: 'role',
    header: 'Role',
    size: 220,
    cell: ({ row: { original: m } }) => (
      <div className="flex flex-wrap items-center gap-1.5">
        <Chip color={ROLE_COLORS[m.role] ?? 'grey'} data-testid={`member-role-${m.id}`}>{roleLabel(m.role)}</Chip>
        {/* Only a vendor org disables members; the member's dialog enables them. */}
        {m.disabled_at && <MemberDisabledChip />}
      </div>
    ),
  },
  {
    id: 'joined',
    header: 'Joined',
    size: 110,
    cell: ({ row: { original: m } }) => (
      <span className="text-xs text-gray-400">{new Date(m.created_at).toLocaleDateString()}</span>
    ),
  },
]

/** "59 members", and how many the filters leave. */
function MembersSummary({ filteredTotal }: { filteredTotal?: number }) {
  // Every member, filtered or not: one row, read for its total.
  const { data } = useQuery({
    queryKey: queryKeys.members.count({}),
    queryFn: () => listMembersPage({}, 0, 1),
  })
  const all = data?.total
  if (all == null) return null
  return (
    <>
      <span data-testid="members-total">{count(all, 'member')}</span>
      {filteredTotal != null && filteredTotal !== all && ` · ${matchCount(filteredTotal)} the filters`}
    </>
  )
}

/**
 * The Members tab's member list: searched, filtered and split into numbered
 * pages on the server, the most powerful role first. The page number sits in
 * the URL (`?page=`). A row opens the member dialog. Owner/admin.
 */
export function MembersCard({ vendor, actions }: {
  vendor: boolean
  /** The header's buttons: the invite popups. */
  actions?: ReactNode
}) {
  const [params] = useSearchParams()
  const filters = useMemo(() => parseMemberFilters(params), [params])
  const paged = usePagedQuery({
    pageKey: MEMBER_PAGE_KEY,
    queryKey: page => queryKeys.members.page(filters, page),
    queryFn: (offset, limit) => listMembersPage(filters, offset, limit),
  })
  const queryClient = useQueryClient()
  const filtered = Object.keys(filters).length > 0
  // The member whose dialog is open. null = closed.
  const [openMember, setOpenMember] = useState<MemberPageItem | null>(null)

  return (
    <>
      <SettingsCard
        title="Members"
        icon={<Users />}
        id="members"
        flush
        action={actions}
        summary={<MembersSummary filteredTotal={filtered ? paged.total : undefined} />}
        toolbar={<MembersFilterBar filters={filters} roles={memberRoles(vendor)} />}
        data-guide="org.members"
      >
        <PagedTable
          paged={paged}
          what="members"
          emptyMessage={filtered ? 'No members match these filters.' : 'No members yet.'}
          columns={MEMBER_COLUMNS}
          onRowClick={m => setOpenMember(m)}
          rowTestId={m => `member-row-${m.id}`}
          rowClassName="cursor-pointer"
          testId="members-pages"
        />
      </SettingsCard>
      {/* key={id}: a fresh dialog per member, so no edits carry over. The list
          reloads after each saved part: a role change can move the row. */}
      {openMember && (
        <MemberDialog
          key={openMember.id}
          member={openMember}
          roles={memberRoles(vendor)}
          onClose={() => setOpenMember(null)}
          onSaved={() => void queryClient.invalidateQueries({ queryKey: queryKeys.members.all })}
        />
      )}
    </>
  )
}
