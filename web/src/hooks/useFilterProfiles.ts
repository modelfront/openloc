import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listFilterProfiles, type FilterProfile, type FilterProfileChoice } from '../api'
import { queryKeys } from '../lib/query-keys'
import { FILTER_FORMATS, type FilterFormat } from '../lib/filter-formats'

/** Every filter profile, grouped by format, built-ins first (the server's order). */
export function useFilterProfiles() {
  const query = useQuery({
    queryKey: queryKeys.filterProfiles.list(),
    queryFn: () => listFilterProfiles(),
  })
  const byFormat = useMemo(() => {
    const out = Object.fromEntries(FILTER_FORMATS.map(f => [f, [] as FilterProfile[]])) as Record<FilterFormat, FilterProfile[]>
    for (const p of query.data ?? []) {
      if (p.format in out) out[p.format as FilterFormat].push(p)
    }
    return out
  }, [query.data])
  return { profiles: query.data ?? [], byFormat, isLoading: query.isLoading, isSuccess: query.isSuccess, error: query.error, refetch: query.refetch }
}

/** The Default built-in of a format: what a project that names none uses. */
export function defaultFilterProfile(profiles: FilterProfile[]): FilterProfile | undefined {
  return profiles.find(p => p.builtin && p.builtin_key?.includes('-default@'))
}

/** The org default per format, as a format → id map: what a new project or
 *  template without a template starts with. */
export function orgDefaultsMap(profiles: FilterProfile[]): Partial<Record<FilterFormat, number>> {
  const out: Partial<Record<FilterFormat, number>> = {}
  for (const p of profiles) if (p.org_default) out[p.format as FilterFormat] = p.id
  return out
}

/** A project's or template's choices as a format → id map. */
export function choicesToMap(choices: FilterProfileChoice[] | null | undefined): Partial<Record<FilterFormat, number>> {
  const out: Partial<Record<FilterFormat, number>> = {}
  for (const c of choices ?? []) out[c.format as FilterFormat] = c.id
  return out
}
