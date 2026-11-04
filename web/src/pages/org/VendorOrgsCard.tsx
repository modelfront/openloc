import { useMemo, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useIsFetching, useQuery, keepPreviousData } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { Building2 } from 'lucide-react'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { Input } from '@/components/ui/input'
import { ClearFiltersButton, LanguageFilterChip, PagedTable } from '@/components/list'
import { getVendorOrgFacets, listVendorOrgs, type VendorOrg, type VendorOrgFilters } from '../../api'
import { MemberDisabledChip } from '@/components/ui/chip-presets'
import { VendorOrgDialog } from './VendorOrgDialog'
import { ORG_VENDORS_KEY } from './org-paths'
import { queryKeys } from '../../lib/query-keys'
import { count, matchCount } from '../../lib/plural'
import { rowsFromPairs, PairRowChip } from '../../components/userpair/LanguagePairsEditor'
import { useAttentionTarget } from '../../hooks/useAttention'
import { useDebouncedUrlParam } from '../../hooks/useDebouncedUrlParam'
import { usePagedQuery } from '../../hooks/usePagedQuery'
import { useUrlParamSetters } from '../../hooks/useUrlParamSetters'

// The Vendors list's URL params, prefixed so they never meet the Members list's.
const VENDOR_PARAMS = { q: 'vq', source: 'vsource', target: 'vtarget' } as const
const VENDOR_PAGE_KEY = 'vpage'

function parseVendorFilters(params: URLSearchParams): VendorOrgFilters {
  const filters: VendorOrgFilters = {}
  for (const [key, param] of Object.entries(VENDOR_PARAMS) as [keyof VendorOrgFilters, string][]) {
    const value = params.get(param)?.trim()
    if (value) filters[key] = value
  }
  return filters
}

/** A vendor's pairs as chips, one per source language ("en → de, fr"). */
function VendorPairs({ org }: { org: VendorOrg }) {
  if (org.language_pairs.length === 0) {
    return <span className="text-xs text-gray-400" data-testid={`vendor-pairs-none-${org.id}`}>—</span>
  }
  const sources = [...new Set(org.language_pairs.map(p => p.source_lang))]
  const rows = rowsFromPairs(org.language_pairs.map(p => ({ ...p, row_group: sources.indexOf(p.source_lang) })))
  return (
    <div className="flex flex-wrap gap-1" data-testid={`vendor-pairs-${org.id}`}>
      {rows.map((row, i) => <PairRowChip key={i} row={row} />)}
    </div>
  )
}

/**
 * The vendor's name and its enabled admins, one a line, so a second one never
 * pushes the first out of view. A Disabled chip by the name says some admins
 * are disabled; the row's window lists and enables them.
 */
function VendorCell({ org: o }: { org: VendorOrg }) {
  const enabled = o.admins.filter(a => !a.disabled)
  return (
    <div className="min-w-0">
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{o.name}</span>
        {enabled.length < o.admins.length && <MemberDisabledChip />}
      </span>
      {enabled.length > 0 ? enabled.map(a => (
        <span key={a.user_id} className="block truncate text-xs text-gray-500 dark:text-gray-400" data-testid={`vendor-admin-${o.id}`}>{a.email}</span>
      )) : (
        <span className="block text-xs text-gray-500 dark:text-gray-400">No active admin</span>
      )}
    </div>
  )
}

const VENDOR_COLUMNS: ColumnDef<VendorOrg>[] = [
  {
    id: 'vendor',
    header: 'Vendor',
    cell: ({ row: { original: o } }) => <VendorCell org={o} />,
  },
  { id: 'languages', header: 'Languages', size: 220, cell: ({ row: { original: o } }) => <VendorPairs org={o} /> },
  {
    id: 'joined',
    header: 'Joined',
    size: 110,
    cell: ({ row: { original: o } }) => (
      <span className="text-xs text-gray-400">{new Date(o.created_at).toLocaleDateString()}</span>
    ),
  },
]

/**
 * The Members tab's Vendors list: the vendor orgs this org invited, searched
 * by name or admin email, filtered by the languages their people cover, in
 * numbered pages. Owner/admin, customer orgs only.
 */
export function VendorOrgsCard({ inviteAction }: {
  /** The header's Invite vendor button. */
  inviteAction?: ReactNode
}) {
  const [params] = useSearchParams()
  const filters = useMemo(() => parseVendorFilters(params), [params])
  const { setParamValues, clearParams } = useUrlParamSetters()
  const [search, setSearch] = useDebouncedUrlParam(VENDOR_PARAMS.q, 300, [VENDOR_PAGE_KEY])
  const setFilter = (key: 'source' | 'target', value: string | null) =>
    setParamValues({ [VENDOR_PARAMS[key]]: value, [VENDOR_PAGE_KEY]: null })

  // The language filters offer only languages some vendor covers, given the others.
  const { data: facets } = useQuery({
    queryKey: queryKeys.vendorOrgs.facets(filters),
    queryFn: () => getVendorOrgFacets(filters),
    placeholderData: keepPreviousData,
  })
  const paged = usePagedQuery({
    pageKey: VENDOR_PAGE_KEY,
    queryKey: page => queryKeys.vendorOrgs.page(filters, page),
    queryFn: (offset, limit) => listVendorOrgs(filters, offset, limit),
  })
  const filtered = Object.keys(filters).length > 0
  // The summary counts every vendor, filtered or not: one row, read for its total.
  const everyone = useQuery({
    queryKey: queryKeys.vendorOrgs.page({}, 0),
    queryFn: () => listVendorOrgs({}, 0, 1),
  })
  // The open vendor, by id: its window follows the reloaded row after Enable.
  const [openId, setOpenId] = useState<number | null>(null)
  const openOrg = paged.rows.find(o => o.id === openId)

  // A link from the bell's vendor notices points here. The section takes it
  // once its rows and the Members list above it are in, so the scroll lands
  // where the section stays.
  const attentionTarget = useAttentionTarget(ORG_VENDORS_KEY)
  const membersLoading = useIsFetching({ queryKey: queryKeys.members.all }) > 0
  const settled = !paged.query.isPending && !membersLoading

  const allTotal = everyone.data?.total
  const summary = allTotal == null ? undefined
    : allTotal === 0 ? 'None yet'
    : `${count(allTotal, 'vendor')}${filtered && paged.total != null ? ` · ${matchCount(paged.total)} the filters` : ''}`

  return (
    <SettingsCard
      ref={settled ? attentionTarget : undefined}
      flush
      title="Vendors"
      icon={<Building2 />}
      summary={summary}
      action={inviteAction}
      toolbar={
        <div className="flex flex-1 flex-wrap items-center gap-2" data-testid="vendor-filter-bar">
          <Input
            placeholder="Search vendor or admin email..."
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="text-sm max-w-xs"
            data-testid="vendor-search"
          />
          <LanguageFilterChip label="Source" value={filters.source ?? null} codes={facets?.sources} onChange={code => setFilter('source', code)} testId="vendor-filter-source" />
          <LanguageFilterChip label="Target" value={filters.target ?? null} codes={facets?.targets} onChange={code => setFilter('target', code)} testId="vendor-filter-target" />
          <ClearFiltersButton
            count={Object.keys(filters).length}
            onClear={() => clearParams([...Object.values(VENDOR_PARAMS), VENDOR_PAGE_KEY])}
            testId="vendor-filter-clear"
          />
        </div>
      }
      data-guide="org.vendor_orgs"
    >
      <PagedTable
        paged={paged}
        what="vendors"
        emptyMessage={filtered ? 'No vendors match these filters.' : 'No vendors yet. Invite one with Invite vendor above.'}
        columns={VENDOR_COLUMNS}
        onRowClick={o => setOpenId(o.id)}
        rowTestId={o => `vendor-org-row-${o.id}`}
        rowClassName="cursor-pointer"
        testId="vendor-pages"
      />
      {openOrg && <VendorOrgDialog key={openOrg.id} org={openOrg} onClose={() => setOpenId(null)} />}
    </SettingsCard>
  )
}
