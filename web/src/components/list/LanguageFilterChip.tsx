import { useMemo, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listLanguages } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { LanguageCode } from '@/components/ui/language-code'
import { FilterChip, FilterOption } from './FilterChip'

/**
 * A filter chip for one language (a source or a target): "All", then every
 * language the org knows, or only `codes` when given (the languages with a
 * hit). The current pick stays listed so it can be seen and cleared. `value`
 * is a language code or null for All.
 */
export function LanguageFilterChip({ label, value, onChange, testId, size = 'md', codes }: {
  label: string
  value: string | null
  onChange: (code: string | null) => void
  testId: string
  size?: 'sm' | 'md'
  codes?: string[]
}) {
  const { data: allLanguages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const languages = useMemo(() => {
    if (!codes) return allLanguages
    const wanted = new Set(value ? [...codes, value] : codes)
    const names = new Map((allLanguages ?? []).map(l => [l.code, l.name]))
    return [...wanted].sort().map(code => ({ code, name: names.get(code) ?? code }))
  }, [allLanguages, codes, value])
  return (
    <FilterChip
      size={size}
      label={label}
      value={value?.toUpperCase() ?? null}
      testId={testId}
      render={(close) => languageOptionRows(languages, value, code => { onChange(code); close() })}
    />
  )
}

/**
 * The option rows of a language filter: "All", then each language as its
 * code chip and name, searchable by both. A function, not a component: the
 * filter search reads the FilterOption rows it is given directly.
 */
export function languageOptionRows(
  languages: { code: string; name: string }[] | undefined,
  value: string | null | undefined,
  onPick: (code: string | null) => void,
): ReactNode {
  return (
    <>
      <FilterOption selected={!value} onClick={() => onPick(null)}>All</FilterOption>
      {(languages ?? []).map(l => (
        <FilterOption key={l.code} selected={value === l.code} keywords={`${l.code} ${l.name}`} onClick={() => onPick(l.code)}>
          <LanguageCode code={l.code} /> — {l.name}
        </FilterOption>
      ))}
    </>
  )
}
