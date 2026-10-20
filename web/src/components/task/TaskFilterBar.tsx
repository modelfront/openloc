// TaskFilterBar — the filter row on the Tasks page: name search, Step and
// Deadline. Filter state lives in the URL (useSearchParams), so a filtered view
// survives a refresh and can be shared; an empty filter removes its param.

import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTaskSteps, TASK_DEADLINE_FILTERS } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { parseTaskFilters, TASK_DEADLINE_LABELS } from '../../lib/task-filters'
import { useDebouncedUrlParam } from '../../hooks/useDebouncedUrlParam'
import { useUrlParamSetters } from '../../hooks/useUrlParamSetters'
import { Input } from '@/components/ui/input'
import { ClearFiltersButton, FilterChip, FilterOption } from '@/components/list'

export function TaskFilterBar() {
  const [params] = useSearchParams()
  const { setParam, clearParams } = useUrlParamSetters()
  const filters = useMemo(() => parseTaskFilters(params), [params])
  const [search, setSearch] = useDebouncedUrlParam('q')

  // Options span the caller's whole task scope, not just the loaded page.
  const { data: steps } = useQuery({ queryKey: queryKeys.tasks.steps, queryFn: listTaskSteps })

  const activeCount = Object.keys(filters).length

  return (
    <div className="flex flex-wrap items-center gap-2 mb-4" data-testid="task-filter-bar">
      <Input
        placeholder="Search tasks..."
        value={search}
        onChange={e => setSearch(e.target.value)}
        className="text-sm max-w-xs"
        data-testid="task-search"
      />

      <FilterChip
        size="md"
        label="Step"
        value={filters.step ?? null}
        testId="filter-step"
        render={(close) => (
          <>
            <FilterOption selected={!filters.step} onClick={() => { setParam('step', null); close() }}>All</FilterOption>
            {(steps ?? []).map(s => (
              <FilterOption key={s} selected={filters.step === s} onClick={() => { setParam('step', s); close() }}>
                {s}
              </FilterOption>
            ))}
          </>
        )}
      />

      <FilterChip
        size="md"
        label="Deadline"
        value={filters.deadline ? TASK_DEADLINE_LABELS[filters.deadline] : null}
        testId="filter-deadline"
        render={(close) => (
          <>
            <FilterOption selected={!filters.deadline} onClick={() => { setParam('deadline', null); close() }}>All</FilterOption>
            {TASK_DEADLINE_FILTERS.map(d => (
              <FilterOption key={d} selected={filters.deadline === d} onClick={() => { setParam('deadline', d); close() }}>
                {TASK_DEADLINE_LABELS[d]}
              </FilterOption>
            ))}
          </>
        )}
      />

      {/* The only params on /tasks are the filters this bar manages. */}
      <ClearFiltersButton count={activeCount} onClear={() => clearParams()} testId="filter-clear-all" />
    </div>
  )
}
