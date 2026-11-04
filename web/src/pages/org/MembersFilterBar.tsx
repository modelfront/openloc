import { Input } from '@/components/ui/input'
import { ClearFiltersButton, FilterChip, FilterOption, LanguageFilterChip } from '@/components/list'
import { useDebouncedUrlParam } from '../../hooks/useDebouncedUrlParam'
import { useUrlParamSetters } from '../../hooks/useUrlParamSetters'
import { ROLE_ORDER, roleLabel } from '../../lib/roles'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { getMemberFacets, type MemberFilters } from '../../api'
import { queryKeys } from '../../lib/query-keys'

/** The URL params the Members tab's filters live in. */
export const MEMBER_FILTER_KEYS = ['q', 'role', 'source', 'target'] as const

/** The Members tab's page number; any filter change goes back to page 1. */
export const MEMBER_PAGE_KEY = 'page'

/** The Members tab's filters from the page URL; an unknown role is ignored. */
export function parseMemberFilters(params: URLSearchParams): MemberFilters {
  const filters: MemberFilters = {}
  for (const key of MEMBER_FILTER_KEYS) {
    const value = params.get(key)?.trim()
    if (value) filters[key] = value
  }
  if (filters.role && !(ROLE_ORDER as readonly string[]).includes(filters.role)) delete filters.role
  return filters
}

/** Search, role and language filters above the Members list. */
export function MembersFilterBar({ filters, roles }: { filters: MemberFilters; roles: string[] }) {
  // Each filter offers only values with at least one member, given the others.
  const { data: facets } = useQuery({
    queryKey: queryKeys.members.facets(filters),
    queryFn: () => getMemberFacets(filters),
    placeholderData: keepPreviousData,
  })
  const roleOptions = facets?.roles
    ? roles.filter(r => facets.roles!.includes(r) || r === filters.role)
    : roles
  const { setParamValues, clearParams } = useUrlParamSetters()
  const setFilter = (key: string, value: string | null) => setParamValues({ [key]: value, [MEMBER_PAGE_KEY]: null })
  // Name or email, debounced into the `q` URL param that drives the list query.
  const [search, setSearch] = useDebouncedUrlParam('q', 300, [MEMBER_PAGE_KEY])
  const activeCount = MEMBER_FILTER_KEYS.filter(key => filters[key]).length

  return (
    <div className="flex flex-1 flex-wrap items-center gap-2" data-testid="member-filter-bar">
      <Input
        placeholder="Search name or email..."
        value={search}
        onChange={e => setSearch(e.target.value)}
        className="text-sm max-w-xs"
        data-testid="member-search"
      />
      <FilterChip
        size="md"
        label="Role"
        value={filters.role ? roleLabel(filters.role) : null}
        testId="filter-role"
        render={(close) => (
          <>
            <FilterOption selected={!filters.role} onClick={() => { setFilter('role', null); close() }}>All</FilterOption>
            {roleOptions.map(r => (
              <FilterOption key={r} selected={filters.role === r} onClick={() => { setFilter('role', r); close() }}>
                {roleLabel(r)}
              </FilterOption>
            ))}
          </>
        )}
      />
      <LanguageFilterChip label="Source" value={filters.source ?? null} codes={facets?.sources} onChange={code => setFilter('source', code)} testId="filter-source" />
      <LanguageFilterChip label="Target" value={filters.target ?? null} codes={facets?.targets} onChange={code => setFilter('target', code)} testId="filter-target" />
      <ClearFiltersButton count={activeCount} onClear={() => clearParams([...MEMBER_FILTER_KEYS, MEMBER_PAGE_KEY])} testId="member-filter-clear" />
    </div>
  )
}
