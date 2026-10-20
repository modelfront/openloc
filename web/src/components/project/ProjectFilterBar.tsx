// ProjectFilterBar — chip-style filter row for the project list.
//
// State lives in URL query params via React Router's useSearchParams so:
//   - Filters survive page refresh.
//   - Filter views are deep-linkable / shareable.
//   - Browser back/forward navigates filter history naturally.
//
// Empty filter values are removed from the URL (no `?status=` with empty
// string) so the URL stays clean when filters are cleared.
//
// Each chip is a popover-trigger Button: closed = label + current value;
// open = options. "All" / "Anyone" entries clear the filter for that key.

import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  listLabels,
  listLanguages,
  listProjectCreators,
  PROJECT_STATUSES,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { parseProjectFilters } from '../../lib/project-filters'
import { Input } from '@/components/ui/input'
import { useDebouncedUrlParam } from '../../hooks/useDebouncedUrlParam'
import { useUrlParamSetters } from '../../hooks/useUrlParamSetters'
import { ClearFiltersButton, CreatedByFilterChip, FilterChip, FilterOption } from '@/components/list'
import { STATUS_LABELS } from './ProjectStatusSelect'
import { LanguageCode } from '@/components/ui/language-code'

interface Props {
  /** Current user id for the "Me" shortcut in the creator chip (null while me query loads). */
  myUserId: number | null
}

export function ProjectFilterBar({ myUserId }: Props) {
  const [params] = useSearchParams()
  const { setParam, clearParams } = useUrlParamSetters()

  const filters = useMemo(() => parseProjectFilters(params), [params])

  // Name search, debounced into the `q` URL param that drives the list query.
  const [search, setSearch] = useDebouncedUrlParam('q')

  // Customer is a built-in label, so the filter's options are its values.
  const { data: labels } = useQuery({ queryKey: queryKeys.labels.all, queryFn: listLabels })
  const customers = labels?.find(l => l.system && l.name === 'Customer')?.values ?? []
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  // Creator options: only people who actually created a project here. See
  // the query's comment in internal/project/queries.sql for why this isn't
  // the org member list.
  const { data: creators } = useQuery({ queryKey: queryKeys.projects.creators, queryFn: listProjectCreators })

  const activeCount = Object.keys(filters).length
  const customerName = filters.customer_id != null
    ? customers.find(c => c.id === filters.customer_id)?.value ?? `#${filters.customer_id}`
    : null

  return (
    <div className="flex flex-wrap items-center gap-2 mb-4" data-testid="project-filter-bar" data-guide="projects.filters">
      <Input
        placeholder="Search projects..."
        value={search}
        onChange={e => setSearch(e.target.value)}
        className="text-sm max-w-xs"
        data-testid="project-search"
      />
      <FilterChip
        size="md"
        label="Status"
        value={filters.status ? STATUS_LABELS[filters.status] : null}
        testId="filter-status"
        render={(close) => (
          <>
            <FilterOption selected={!filters.status} onClick={() => { setParam('status', null); close() }}>All</FilterOption>
            {PROJECT_STATUSES.map(s => (
              <FilterOption key={s} selected={filters.status === s} onClick={() => { setParam('status', s); close() }}>
                {STATUS_LABELS[s]}
              </FilterOption>
            ))}
          </>
        )}
      />

      <FilterChip
        size="md"
        label="Customer"
        value={customerName}
        testId="filter-customer"
        render={(close) => (
          <>
            <FilterOption selected={filters.customer_id == null} onClick={() => { setParam('customer_id', null); close() }}>All</FilterOption>
            {customers.map(c => (
              <FilterOption key={c.id} selected={filters.customer_id === c.id} onClick={() => { setParam('customer_id', String(c.id)); close() }}>
                {c.value}
              </FilterOption>
            ))}
          </>
        )}
      />

      <FilterChip
        size="md"
        label="Source"
        value={filters.source?.toUpperCase() ?? null}
        testId="filter-source"
        render={(close) => (
          <>
            <FilterOption selected={!filters.source} onClick={() => { setParam('source', null); close() }}>All</FilterOption>
            {(languages ?? []).map(l => (
              <FilterOption key={l.code} selected={filters.source === l.code} onClick={() => { setParam('source', l.code); close() }}>
                <LanguageCode code={l.code} /> — {l.name}
              </FilterOption>
            ))}
          </>
        )}
      />

      <FilterChip
        size="md"
        label="Target"
        value={filters.target?.toUpperCase() ?? null}
        testId="filter-target"
        render={(close) => (
          <>
            <FilterOption selected={!filters.target} onClick={() => { setParam('target', null); close() }}>All</FilterOption>
            {(languages ?? []).map(l => (
              <FilterOption key={l.code} selected={filters.target === l.code} onClick={() => { setParam('target', l.code); close() }}>
                <LanguageCode code={l.code} /> — {l.name}
              </FilterOption>
            ))}
          </>
        )}
      />



      {/* Creator picker: a manager filters by any teammate, not just
          themselves. "Me" is one click inside the popover. */}
      <CreatedByFilterChip
        size="md"
        value={filters.created_by ?? null}
        myUserId={myUserId}
        options={creators ?? []}
        onChange={id => setParam('created_by', id == null ? null : String(id))}
      />

      {/* The only params on /projects are the filters this bar manages, so a
          full reset is safe. */}
      <ClearFiltersButton count={activeCount} onClear={() => clearParams()} testId="filter-clear-all" />
    </div>
  )
}
