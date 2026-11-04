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
//
// Status, Deadline and Created by, the filters a PM uses daily, always show
// their chip. The others sit behind the filter icon (AddFilterMenu) and show a chip only
// while set, before the icon, so the bar stays one row inside the page's
// width and the icon keeps its place at the end.

import { useMemo, type ReactNode } from 'react'
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
import { AddFilterMenu, ClearFiltersButton, CreatedByFilterChip, FilterChip, FilterOption, languageOptionRows } from '@/components/list'
import { STATUS_LABELS } from './ProjectStatusSelect'
import { Chip } from '@/components/ui/chip'
import {
  DEADLINE_BAND_COLORS,
  DEADLINE_FILTER_BANDS,
  DEADLINE_FILTER_LABELS,
  type DeadlineFilterBand,
} from '../../lib/deadline'

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

  // Deadline takes several bands at once ("Overdue" + "Due within 24h" is a
  // PM's daily view), kept in the URL in DEADLINE_FILTER_BANDS order.
  const deadlines = filters.deadline ?? []
  const toggleDeadline = (band: DeadlineFilterBand) => {
    const next = DEADLINE_FILTER_BANDS.filter(b => (b === band) !== deadlines.includes(b))
    setParam('deadline', next.length ? next.join(',') : null)
  }

  // The filters behind the icon, each once: the chip shows its value and the
  // icon's menu offers the same option rows.
  const languageOptions = (param: 'source' | 'target', current: string | undefined) => (close: () => void) =>
    languageOptionRows(languages, current, code => { setParam(param, code); close() })
  const more: { key: string; param: string; label: string; value: string | null; options: (close: () => void) => ReactNode }[] = [
    {
      key: 'customer', param: 'customer_id', label: 'Customer', value: customerName,
      options: close => (
        <>
          <FilterOption selected={filters.customer_id == null} onClick={() => { setParam('customer_id', null); close() }}>All</FilterOption>
          {customers.map(c => (
            <FilterOption key={c.id} selected={filters.customer_id === c.id} onClick={() => { setParam('customer_id', String(c.id)); close() }}>
              {c.value}
            </FilterOption>
          ))}
        </>
      ),
    },
    { key: 'source', param: 'source', label: 'Source', value: filters.source?.toUpperCase() ?? null, options: languageOptions('source', filters.source) },
    { key: 'target', param: 'target', label: 'Target', value: filters.target?.toUpperCase() ?? null, options: languageOptions('target', filters.target) },
  ]
  const unset = more.filter(f => f.value == null)

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
        searchable={false}
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
        label="Deadline"
        value={deadlines.length ? deadlines.map(b => DEADLINE_FILTER_LABELS[b]).join(', ') : null}
        testId="filter-deadline"
        searchable={false}
        render={(close) => (
          <>
            <FilterOption selected={!deadlines.length} onClick={() => { setParam('deadline', null); close() }} data-testid="filter-deadline-all">All</FilterOption>
            {/* Each option is the chip the list shows for that band. */}
            {DEADLINE_FILTER_BANDS.map(b => (
              <FilterOption key={b} multi selected={deadlines.includes(b)} onClick={() => toggleDeadline(b)} data-testid={`filter-deadline-${b}`}>
                <Chip color={DEADLINE_BAND_COLORS[b]}>{DEADLINE_FILTER_LABELS[b]}</Chip>
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

      {more.filter(f => f.value != null).map(f => (
        <FilterChip
          key={f.key}
          size="md"
          label={f.label}
          value={f.value}
          testId={`filter-${f.key}`}
          onRemove={() => setParam(f.param, null)}
          render={f.options}
        />
      ))}

      {unset.length > 0 && (
        <AddFilterMenu
          size="md"
          testId="project-filter-menu"
          filters={unset.map(f => ({ key: f.key, label: f.label, options: f.options }))}
        />
      )}
      {/* The only params on /projects are the filters this bar manages, so a
          full reset is safe. */}
      <ClearFiltersButton count={activeCount} onClear={() => clearParams()} testId="filter-clear-all" />
    </div>
  )
}
